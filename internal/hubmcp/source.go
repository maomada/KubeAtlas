package hubmcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/maomada/KubeAtlas/internal/hub"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var errSourceLimit = errors.New("source response limit exceeded")

type source struct {
	agent     hub.Agent
	session   *mcp.ClientSession
	transport *boundedTransport
	readOnly  bool
}

type boundedTransport struct {
	base        http.RoundTripper
	requestID   string
	exceeded    atomic.Bool
	status      atomic.Int32
	tunnelError atomic.Pointer[hub.Error]
}

func (t *boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Request-ID", t.requestID)
	response, err := t.base.RoundTrip(r)
	if err != nil {
		var tunnelError *hub.Error
		if errors.As(err, &tunnelError) {
			t.tunnelError.Store(tunnelError)
		}
		return nil, err
	}
	t.status.Store(int32(response.StatusCode))
	if response.ContentLength > maxSourceBytes {
		t.exceeded.Store(true)
		_ = response.Body.Close()
		return nil, errSourceLimit
	}
	response.Body = &boundedBody{ReadCloser: response.Body, left: maxSourceBytes, exceeded: &t.exceeded}
	return response, nil
}

type boundedBody struct {
	io.ReadCloser
	left     int
	exceeded *atomic.Bool
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, errSourceLimit
	}
	if len(p) == 0 {
		return 0, nil
	}
	if len(p) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.left -= n
	if b.left < 0 {
		b.exceeded.Store(true)
		_ = b.Close()
		return 0, errSourceLimit
	}
	return n, err
}

func (f *facade) agent(id string) (hub.Agent, bool) {
	for _, agent := range f.hub.Agents() {
		if agent.ClusterID == id {
			return agent, true
		}
	}
	return hub.Agent{}, false
}

func (f *facade) open(ctx context.Context, p hub.Principal, clusterID, requestID string) (*source, error) {
	if !slices.Contains(p.ClusterIDs, clusterID) {
		return nil, &fault{code: "permission_denied", message: "Cluster is outside this credential's scope."}
	}
	cluster, exists := f.store.Cluster(clusterID)
	if !exists || cluster.RevokedAt != nil {
		return nil, &fault{code: "cluster_unavailable", message: "Cluster is not available for live requests."}
	}
	agent, connected := f.agent(clusterID)
	if !connected {
		return nil, &fault{code: "cluster_unavailable", message: "Cluster Agent is offline.", next: "Reconnect the configured Agent, then retry a read.", retryable: true}
	}
	connect := func(principal hub.Principal) (*source, error) {
		client, err := f.hub.HTTPClient(clusterID, principal)
		if err != nil {
			return nil, classify(ctx, nil, err, false)
		}
		current, ok := f.agent(clusterID)
		if !ok || current.ConnectionID != agent.ConnectionID {
			return nil, &fault{code: "source_changed", message: "Cluster connection changed before dispatch.", next: "Refresh the directory and retry."}
		}
		transport := &boundedTransport{base: client.Transport, requestID: requestID}
		client.Transport = transport
		path := "/mcp"
		if principal.ReadOnly {
			path = "/mcp-readonly"
		}
		result := &source{agent: agent, transport: transport, readOnly: principal.ReadOnly}
		result.session, err = mcp.NewClient(&mcp.Implementation{Name: "kubeatlas-hub", Version: "1"}, nil).Connect(ctx,
			&mcp.StreamableClientTransport{Endpoint: "http://localhost" + path, HTTPClient: client, DisableStandaloneSSE: true, MaxRetries: -1, MaxEventSize: maxSourceBytes}, nil)
		if err != nil {
			return result, classify(ctx, result, err, false)
		}
		return result, nil
	}
	result, err := connect(p)
	// A source configured read-only has no /mcp route. Only initialization may
	// fall back; no tool call or failed write is ever retried.
	if err != nil && !p.ReadOnly && result != nil && result.transport.status.Load() == http.StatusNotFound && !result.transport.exceeded.Load() {
		if result.session != nil {
			_ = result.session.Close()
		}
		p.ReadOnly = true
		return connect(p)
	}
	return result, err
}

func classify(ctx context.Context, s *source, err error, mayWrite bool) error {
	problem := &fault{code: "source_error", message: "Source did not return a complete MCP result.", next: "Check source connectivity and permissions.", unknown: mayWrite}
	var tunnelError *hub.Error
	if s != nil {
		switch s.transport.status.Load() {
		case http.StatusUnauthorized, http.StatusForbidden:
			problem.code, problem.message = "permission_denied", "Source rejected access for this identity."
		case http.StatusNotFound:
			problem.code, problem.message = "source_mcp_unavailable", "Source MCP endpoint is unavailable."
		}
		if s.transport.exceeded.Load() {
			problem.code, problem.message = "response_too_large", "Source response exceeded its 1 MiB transport limit."
			problem.next = "Reduce the source page size or requested detail."
		}
		tunnelError = s.transport.tunnelError.Load()
	}
	if tunnelError == nil {
		_ = errors.As(err, &tunnelError)
	}
	if tunnelError != nil {
		problem.code, problem.message = string(tunnelError.Code), tunnelError.Error()
		problem.unknown = mayWrite && tunnelError.OutcomeUnknown
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		problem.code, problem.message = "source_timeout", "The live request exceeded its time budget."
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		problem.code, problem.message = "source_cancelled", "The live request was cancelled."
	}
	if problem.unknown {
		problem.next = "The operation may have taken effect. Reconcile the target before retrying; no automatic replay was performed."
	}
	problem.retryable = !mayWrite && (problem.code == "source_timeout" || problem.code == "source_disconnected" || problem.code == "cluster_unavailable")
	return problem
}

func (s *source) tools(ctx context.Context) ([]*mcp.Tool, string, error) {
	tools := make([]*mcp.Tool, 0)
	bytesRead := 0
	cursor, seen := "", map[string]bool{}
	names := map[string]bool{}
	for pages := 0; pages < 8; pages++ {
		result, err := s.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, "", classify(ctx, s, err, false)
		}
		if result == nil {
			break
		}
		encoded, encodeErr := json.Marshal(result.Tools)
		bytesRead += len(encoded)
		if encodeErr != nil || bytesRead > maxSourceBytes {
			return nil, "", &fault{code: "response_too_large", message: "Source catalog exceeds its total byte budget."}
		}
		for _, tool := range result.Tools {
			if tool == nil || !toolNamePattern.MatchString(tool.Name) || names[tool.Name] {
				return nil, "", &fault{code: "source_protocol_error", message: "Source catalog has invalid or duplicate tool names."}
			}
			names[tool.Name] = true
			tools = append(tools, tool)
		}
		if len(tools) > maxTools {
			return nil, "", &fault{code: "response_too_large", message: "Source catalog exceeds 256 tools."}
		}
		if result.NextCursor == "" {
			slices.SortFunc(tools, func(a, b *mcp.Tool) int { return strings.Compare(a.Name, b.Name) })
			return tools, fingerprint(tools), nil
		}
		if len(result.NextCursor) > 4096 || seen[result.NextCursor] {
			break
		}
		seen[result.NextCursor], cursor = true, result.NextCursor
	}
	return nil, "", &fault{code: "source_protocol_error", message: "Source catalog pagination did not terminate within its bound."}
}
