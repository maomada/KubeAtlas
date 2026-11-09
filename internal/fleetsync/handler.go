package fleetsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxRequestBytes = 1 << 20

func NewHandler(store *Store) http.Handler {
	return NewHandlerWithBridge(store, nil)
}

func NewHandlerWithBridge(store *Store, bridge ToolBridge) http.Handler {
	return newHandler(store, bridge, nil)
}

// NewHandlerWithMCPHosts allows exact external authorities behind a trusted proxy.
// It replaces the SDK's loopback-only check with an explicit Host allowlist.
func NewHandlerWithMCPHosts(store *Store, bridge ToolBridge, hosts []string) (http.Handler, error) {
	allowed := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		parsed, err := url.Parse("https://" + host)
		if err != nil || host == "" || strings.ContainsAny(host, "*\\ \t\r\n") || parsed.Host != host ||
			parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(host, "#") {
			return nil, errors.New("Fleet MCP allowed hosts must be exact host[:port] authorities without wildcards")
		}
		if strings.HasSuffix(host, ":") {
			return nil, errors.New("Fleet MCP allowed host has an empty port")
		}
		if port := parsed.Port(); port != "" {
			value, err := strconv.Atoi(port)
			if err != nil || value < 1 || value > 65535 {
				return nil, errors.New("Fleet MCP allowed host has an invalid port")
			}
		}
		allowed[strings.ToLower(host)] = true
	}
	return newHandler(store, bridge, allowed), nil
}

func newHandler(store *Store, bridge ToolBridge, allowedHosts map[string]bool) http.Handler {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "kubeatlas-fleet-sync", Version: "1"}, nil)
	registerTools(mcpServer, store, bridge)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: maxRequestBytes,
		DisableLocalhostProtection: len(allowedHosts) > 0,
	})
	protected := http.NewCrossOriginProtection().Handler(mcpHandler)
	return &handler{store: store, bridge: bridge, mcp: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(allowedHosts) > 0 && !allowedHosts[strings.ToLower(r.Host)] && !loopbackHost(r.Host) {
			http.Error(w, "forbidden MCP Host", http.StatusForbidden)
			return
		}
		protected.ServeHTTP(w, r)
	})}
}

func loopbackHost(authority string) bool {
	host := authority
	if parsed, _, err := net.SplitHostPort(authority); err == nil {
		host = parsed
	}
	return strings.EqualFold(host, "localhost") || net.ParseIP(strings.Trim(host, "[]")).IsLoopback()
}

type handler struct {
	store  *Store
	bridge ToolBridge
	mcp    http.Handler
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("Fleet synchronization is unavailable"))
		return
	}
	if r.URL.Path == "/mcp-fleet" {
		h.mcp.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/healthz" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	switch r.URL.Path {
	case "/api/snapshots":
		NewSnapshotHandler(h.store, "").ServeHTTP(w, r)
	case "/api/clusters":
		if requireMethod(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, http.StatusOK, h.store.Inventory())
	case "/api/fleets":
		if requireMethod(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, http.StatusOK, h.store.Groups())
	case "/api/context":
		if requireMethod(w, r, http.MethodPost) {
			return
		}
		var selection Selection
		if !decodeRequest(w, r, maxRequestBytes, &selection) {
			return
		}
		result, err := h.store.Context(selection)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "/api/resource-context":
		if requireMethod(w, r, http.MethodPost) {
			return
		}
		var selection ResourceSelection
		if !decodeRequest(w, r, maxRequestBytes, &selection) {
			return
		}
		result, err := h.store.Resource(selection)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "/api/tools":
		if requireMethod(w, r, http.MethodPost) {
			return
		}
		var selection Selection
		if !decodeRequest(w, r, maxRequestBytes, &selection) {
			return
		}
		if h.bridge == nil {
			writeError(w, http.StatusServiceUnavailable, errors.New("live source tools are unavailable"))
			return
		}
		result, err := h.bridge.ListClusterTools(r.Context(), selection)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "/api/call-tool":
		if requireMethod(w, r, http.MethodPost) {
			return
		}
		var request ClusterToolCall
		if !decodeRequest(w, r, maxRequestBytes, &request) {
			return
		}
		if h.bridge == nil {
			writeError(w, http.StatusServiceUnavailable, errors.New("live source tools are unavailable"))
			return
		}
		result, err := h.bridge.CallClusterTool(r.Context(), request)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, ErrAgentUnavailable) {
				status = http.StatusServiceUnavailable
			}
			writeError(w, status, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		if strings.HasPrefix(r.URL.Path, "/api/fleets/") {
			h.serveFleet(w, r)
			return
		}
		writeError(w, http.StatusNotFound, errors.New("not found"))
	}
}

func (h *handler) serveFleet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/fleets/")
	if id == "" || strings.Contains(id, "/") || !ValidateID(id) {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	switch r.Method {
	case http.MethodPut:
		var group Group
		if !decodeRequest(w, r, maxRequestBytes, &group) {
			return
		}
		if group.ID != id {
			writeError(w, http.StatusBadRequest, errors.New("path and body fleet IDs must match"))
			return
		}
		if err := h.store.PutGroup(group); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case http.MethodDelete:
		if err := h.store.DeleteGroup(id); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func registerTools(server *mcp.Server, store *Store, bridge ToolBridge) {
	mcp.AddTool[struct{}, any](server, &mcp.Tool{
		Name:        "list_clusters",
		Description: "List synchronized cluster observations and centrally configured multi-cloud groups. No live Kubernetes queries.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		result := struct {
			Clusters []ClusterContext `json:"clusters"`
			Fleets   []Group          `json:"fleets"`
		}{Clusters: store.Inventory(), Fleets: store.Groups()}
		return textResult(result)
	})
	mcp.AddTool[Selection, any](server, &mcp.Tool{
		Name:        "get_agent_context",
		Description: "Read synchronized KubeAtlas context for exactly one clusterId or fleetId. Original payloads stay inside cluster envelopes. Membership is not proof of cross-cluster resource relationships. Resource detail is retrieved with get_resource_context.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, selection Selection) (*mcp.CallToolResult, any, error) {
		result, err := store.Context(selection)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})
	mcp.AddTool[ResourceSelection, any](server, &mcp.Tool{
		Name:        "get_resource_context",
		Description: "Read the original synchronized KubeAtlas get_resource result for one explicit clusterId, plural resource kind, API group, namespace and name. Inspect freshness and limitations; unavailable data is not evidence of absence.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, selection ResourceSelection) (*mcp.CallToolResult, any, error) {
		result, err := store.Resource(selection)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})
	mcp.AddTool[Selection, any](server, &mcp.Tool{
		Name:        "list_cluster_tools",
		Description: "List original KubeAtlas MCP tool catalogs for exactly one clusterId or fleetId. Each source retains its configured read-only and exec mode; unavailable members have an empty catalog.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, selection Selection) (*mcp.CallToolResult, any, error) {
		if bridge == nil {
			return nil, nil, errors.New("live source tools are unavailable")
		}
		result, err := bridge.ListClusterTools(ctx, selection)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result)
	})
	mcp.AddTool[ClusterToolCall, any](server, &mcp.Tool{
		Name:        "call_cluster_tool",
		Description: "Invoke one original MCP tool on one explicit clusterId, optionally requiring membership in fleetId. This may perform mutations; calls are never broadcast. If the source disconnects or times out after dispatch, the outcome is unknown and must be reconciled before retrying.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, request ClusterToolCall) (*mcp.CallToolResult, any, error) {
		if bridge == nil {
			return nil, nil, errors.New("live source tools are unavailable")
		}
		result, err := bridge.CallClusterTool(ctx, request)
		if err != nil {
			return nil, nil, err
		}
		return result, nil, nil
	})
}

func NewSnapshotHandler(store *Store, expectedClusterID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if requireMethod(w, r, http.MethodPost) {
			return
		}
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, errors.New("Fleet synchronization is unavailable"))
			return
		}
		var snapshot Snapshot
		if !decodeRequest(w, r, MaxSnapshotBytes, &snapshot) {
			return
		}
		if expectedClusterID != "" && snapshot.ClusterID != expectedClusterID {
			writeError(w, http.StatusBadRequest, errors.New("snapshot clusterId does not match the upload route"))
			return
		}
		if err := store.Ingest(snapshot); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
}

func textResult(value any) (*mcp.CallToolResult, any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil, nil
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return false
	}
	writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	return true
}

func decodeRequest(w http.ResponseWriter, r *http.Request, limit int64, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, errors.New("Content-Type must be application/json"))
		return false
	}
	reader := http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, errors.New("request body exceeds the size limit"))
		} else {
			writeError(w, http.StatusBadRequest, errors.New("invalid JSON request"))
		}
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, errors.New("request must contain one JSON value"))
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"response encoding failed"}`)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.Copy(w, bytes.NewReader(data))
}
