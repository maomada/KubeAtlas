package hubmcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/cloud"
	"github.com/maomada/KubeAtlas/internal/hub"
	"github.com/maomada/KubeAtlas/internal/hubstate"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func setupHubMCP(t *testing.T) (*facade, hub.Principal, *atomic.Int32) {
	t.Helper()
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	store, err := hubstate.OpenStore(filepath.Join(t.TempDir(), "hub", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	transport, err := hub.New(hub.Config{AuthenticateAgent: func(_ context.Context, token, id string) (hub.AgentIdentity, error) {
		c, err := store.AuthenticateAgent(token, id)
		return hub.AgentIdentity{ClusterID: c.ID, Name: c.Name}, err
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	server := httptest.NewServer(transport)
	t.Cleanup(server.Close)
	var writes atomic.Int32
	makeSource := func(readonly bool) http.Handler {
		s := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
		mcp.AddTool(s, &mcp.Tool{Name: "echo", Description: "Same catalog on both sources", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(_ context.Context, _ *mcp.CallToolRequest, input struct {
			Value string `json:"value,omitempty"`
		}) (*mcp.CallToolResult, any, error) { return &mcp.CallToolResult{Meta: mcp.Meta{"kubeatlas/observedAt": "2026-01-01T00:00:00Z"}, Content: []mcp.Content{&mcp.TextContent{Text: input.Value}}}, nil, nil })
		mcp.AddTool(s, &mcp.Tool{Name: "list_resources", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(_ context.Context, _ *mcp.CallToolRequest, input struct {
			Limit int `json:"limit,omitempty"`
		}) (*mcp.CallToolResult, any, error) { body, _ := json.Marshal(input); return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}, nil, nil })
		if !readonly {
			mcp.AddTool(s, &mcp.Tool{Name: "mutate"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
				writes.Add(1)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", maxSourceBytes+1024)}}}, nil, nil
			})
		}
		return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	}
	full, ro := makeSource(false), makeSource(true)
	for _, id := range []string{"alpha", "beta"} {
		issued, err := store.CreateCluster(hubstate.ClusterInput{ID: id, Name: id})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- cloud.Run(ctx, cloud.Config{URL: "ws" + strings.TrimPrefix(server.URL, "http") + "/agent", Token: issued.Token, ClusterID: id, ClusterName: id, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Forwarded-User") != "kubeatlas:test" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("source identity or credential isolation failed")
				}
				if r.URL.Path == "/mcp-readonly" {
					ro.ServeHTTP(w, r)
					return
				}
				full.ServeHTTP(w, r)
			})})
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("fixture Agent did not stop")
			}
		})
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(transport.Agents()) != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(transport.Agents()) != 2 {
		t.Fatal("fixture Agents did not connect")
	}
	f := &facade{hub: transport, store: store, pages: newPager()}
	p := hub.Principal{CredentialID: "fixture", UserID: "kubeatlas:test", ClusterIDs: []string{"alpha", "beta"}, ReadOnly: true}
	return f, p, &writes
}

func newEnvelope() *envelope { return &envelope{RequestID: "fixture", Source: "hub"} }

func TestLiveHubCatalogDeduplicationAndCursor(t *testing.T) {
	f, p, _ := setupHubMCP(t)
	e := newEnvelope()
	input := listInput{Limit: 1}
	if err := f.listTools(t.Context(), p, e, input); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(e.Data)
	var data struct {
		Clusters []directoryEntry
		Catalogs []catalogReference
		Tools    []toolSummary
		Page     page
	}
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Clusters) != 2 || len(data.Catalogs) != 1 || len(data.Tools) != 1 || !data.Page.Truncated || strings.Contains(string(body), "inputSchema") {
		t.Fatal("catalog not deduplicated, compact, or paged")
	}
	input.Cursor = data.Page.NextCursor
	if err := f.listTools(t.Context(), p, newEnvelope(), input); err != nil {
		t.Fatal(err)
	}
	changed := p
	changed.CredentialID = "another"
	if err := f.listTools(t.Context(), changed, newEnvelope(), input); faultCode(err) != "invalid_cursor" {
		t.Fatalf("credential cursor binding: %v", err)
	}
	f.hub.Disconnect("beta")
	if err := f.listTools(t.Context(), p, newEnvelope(), input); faultCode(err) != "source_changed" {
		t.Fatalf("generation binding: %v", err)
	}
}

func TestLiveHubScopePayloadAndWriteOutcome(t *testing.T) {
	f, p, writes := setupHubMCP(t)
	e := newEnvelope()
	if err := f.callTool(t.Context(), p, e, callInput{ClusterID: "alpha", Tool: "echo", Arguments: map[string]json.RawMessage{"value": json.RawMessage(`"untouched"`)}}); err != nil {
		t.Fatal(err)
	}
	if e.ClusterID != "alpha" || e.ConnectionID == "" || e.ObservedAt == nil || e.ObservedAt.Year() != 2026 || e.Data.(*mcp.CallToolResult).Content[0].(*mcp.TextContent).Text != "untouched" {
		t.Fatal("payload/provenance changed")
	}
	for _, tc := range []struct {
		input callInput
		code  string
	}{
		{callInput{ClusterID: "gamma", Tool: "echo"}, "permission_denied"},
		{callInput{ClusterID: "alpha", Tool: "_invalid"}, "invalid_parameters"},
		{callInput{ClusterID: "alpha", Tool: "missing"}, "tool_not_found"},
		{callInput{ClusterID: "alpha", Tool: "mutate"}, "tool_not_found"},
		{callInput{ClusterID: "alpha", Tool: "echo", CatalogHash: strings.Repeat("0", 64)}, "source_changed"},
	} {
		if err := f.callTool(t.Context(), p, newEnvelope(), tc.input); faultCode(err) != tc.code {
			t.Fatalf("got %v, want %s", err, tc.code)
		}
	}
	e = newEnvelope()
	if err := f.callTool(t.Context(), p, e, callInput{ClusterID: "alpha", Tool: "list_resources"}); err != nil {
		t.Fatal(err)
	}
	if e.Data.(*mcp.CallToolResult).Content[0].(*mcp.TextContent).Text != `{"limit":50}` {
		t.Fatal("source-side default limit absent")
	}
	p.ReadOnly = false
	e = newEnvelope()
	err := f.callTool(t.Context(), p, e, callInput{ClusterID: "alpha", Tool: "mutate"})
	result := finish(e, time.Now(), err)
	if !result.IsError || e.ErrorCode != "response_too_large" || e.Outcome != "unknown" || e.Retryable || writes.Load() != 1 {
		t.Fatalf("write was retried or outcome overclaimed: code=%s outcome=%s writes=%d", e.ErrorCode, e.Outcome, writes.Load())
	}
}

func TestLiveHubHTTPPrincipalAndSchemaErrors(t *testing.T) {
	f, p, _ := setupHubMCP(t)
	h, err := New(f.hub, f.store)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "http://localhost/mcp-fleet", nil))
	if w.Code != 401 {
		t.Fatal("unauthenticated request admitted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(hub.ContextWithPrincipal(r.Context(), p)))
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	valid, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "call_cluster_tool", Arguments: map[string]any{"clusterId": "alpha", "tool": "echo", "arguments": map[string]any{"value": "round trip"}}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(valid)
	if err != nil || valid.IsError || !strings.Contains(string(encoded), "round trip") {
		t.Fatal("native JSON arguments did not survive HTTP schema validation and routing")
	}
	for _, args := range []map[string]any{{"clusterId": "alpha", "tool": "echo", "unexpected": "sensitive-input"}, {"clusterId": "alpha", "tool": "_bad"}} {
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "call_cluster_tool", Arguments: args})
		if err != nil || !result.IsError {
			t.Fatalf("schema error not a tool error: %v", err)
		}
		body, _ := json.Marshal(result)
		if !strings.Contains(string(body), "invalid_parameters") || strings.Contains(string(body), "sensitive-input") {
			t.Fatal("unstructured or sensitive schema error")
		}
	}
}

func TestHubBoundsAndCursorExpiry(t *testing.T) {
	var exceeded atomic.Bool
	body := &boundedBody{ReadCloser: io.NopCloser(strings.NewReader("12345")), left: 4, exceeded: &exceeded}
	if _, err := io.ReadAll(body); err != errSourceLimit || !exceeded.Load() {
		t.Fatal("body limit not enforced")
	}
	p := newPager()
	now := time.Now()
	p.now = func() time.Time { return now }
	_, first, err := pageRows(p, []int{1, 2, 3}, 1, "", "query", "version")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := pageRows(p, []int{1, 2, 3}, 1, first.NextCursor+"x", "query", "version"); faultCode(err) != "invalid_cursor" {
		t.Fatal("tamper accepted")
	}
	now = now.Add(9 * time.Minute)
	_, second, err := pageRows(p, []int{1, 2, 3}, 1, first.NextCursor, "query", "version")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, _, err := pageRows(p, []int{1, 2, 3}, 1, second.NextCursor, "query", "version"); faultCode(err) != "cursor_expired" {
		t.Fatal("cursor expiry extended")
	}
	e := newEnvelope()
	e.Data = strings.Repeat("x", maxResultBytes)
	result := finish(e, time.Now(), nil)
	encoded, _ := json.Marshal(result)
	if !result.IsError || e.ErrorCode != "response_too_large" || len(encoded) > maxResultBytes {
		t.Fatal("result budget missing")
	}
}
