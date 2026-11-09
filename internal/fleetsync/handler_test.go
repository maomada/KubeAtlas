package fleetsync

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFleetMCPAllowedHostsValidation(t *testing.T) {
	for _, host := range []string{"", "*", "*.example.com", "https://portal.example", "portal.example/path", "user@portal.example", "portal.example?", "portal.example#", "portal.example:", "portal.example:abc", "portal.example:0", "portal.example:65536", "portal.example\n"} {
		t.Run(host, func(t *testing.T) {
			if _, err := NewHandlerWithMCPHosts(nil, nil, []string{host}); err == nil {
				t.Fatalf("accepted invalid authority %q", host)
			}
		})
	}
	for _, host := range []string{"portal.example", "portal.example:443", "10.0.101.165:9445", "[2001:db8::1]:9445"} {
		if _, err := NewHandlerWithMCPHosts(nil, nil, []string{host}); err != nil {
			t.Fatalf("rejected authority %q: %v", host, err)
		}
	}
}

func newTestHandler(t *testing.T) (*Store, http.Handler) {
	t.Helper()
	store, err := OpenStore(testStatePath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, id := range []string{"east", "west"} {
		if err := store.Ingest(sampleCluster(id, now).Snapshot); err != nil {
			t.Fatal(err)
		}
	}
	return store, NewHandler(store)
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	if value != nil {
		if err := json.NewEncoder(&body).Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &body)
	if value != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestHTTPAPISynchronizesSnapshotsAndGroupChanges(t *testing.T) {
	store, handler := newTestHandler(t)
	group := Group{ID: "prod", Name: "Production", ClusterIDs: []string{"east", "west"}}
	response := requestJSON(t, handler, http.MethodPut, "/api/fleets/prod", group)
	if response.Code != http.StatusOK {
		t.Fatalf("PUT group: status %d body %s", response.Code, response.Body)
	}
	response = requestJSON(t, handler, http.MethodGet, "/api/clusters", nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET clusters: status %d headers %v", response.Code, response.Header())
	}
	var inventory []ClusterContext
	if err := json.Unmarshal(response.Body.Bytes(), &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 2 || inventory[0].ClusterID != "east" || len(inventory[0].Documents) != 0 {
		t.Fatalf("unexpected inventory: %+v", inventory)
	}
	response = requestJSON(t, handler, http.MethodGet, "/api/fleets", nil)
	var fleets []Group
	if err := json.Unmarshal(response.Body.Bytes(), &fleets); err != nil || !reflect.DeepEqual(fleets, []Group{group}) {
		t.Fatalf("GET fleets = %+v, err = %v", fleets, err)
	}

	response = requestJSON(t, handler, http.MethodPost, "/api/context", Selection{FleetID: "prod"})
	var context AgentContext
	if err := json.Unmarshal(response.Body.Bytes(), &context); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(context.Clusters) != 2 || context.Clusters[0].ClusterID != "east" {
		t.Fatalf("fleet context = %+v, status = %d", context, response.Code)
	}
	if err := store.PutGroup(Group{ID: "prod", Name: "Production", ClusterIDs: []string{"west"}}); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, handler, http.MethodPost, "/api/context", Selection{FleetID: "prod"})
	if err := json.Unmarshal(response.Body.Bytes(), &context); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(context.Clusters) != 1 || context.Clusters[0].ClusterID != "west" {
		t.Fatalf("edited fleet context = %+v, status = %d", context, response.Code)
	}
}

func TestHTTPAPIRejectsMismatchedAndInvalidRequestsAsJSON(t *testing.T) {
	_, handler := newTestHandler(t)
	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"path mismatch", http.MethodPut, "/api/fleets/prod", `{"id":"other","name":"Production","clusterIds":[]}`, http.StatusBadRequest},
		{"ambiguous context", http.MethodPost, "/api/context", `{"clusterId":"east","fleetId":"prod"}`, http.StatusBadRequest},
		{"unknown field", http.MethodPost, "/api/context", `{"clusterId":"east","extra":true}`, http.StatusBadRequest},
		{"trailing JSON", http.MethodPost, "/api/context", `{"clusterId":"east"} {}`, http.StatusBadRequest},
		{"unknown route", http.MethodGet, "/api/unknown", "", http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || !strings.Contains(response.Header().Get("Content-Type"), "application/json") || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d headers=%v body=%q", response.Code, response.Header(), response.Body)
			}
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["error"] == "" {
				t.Fatalf("expected JSON error object, got %q (%v)", response.Body, err)
			}
		})
	}
}

func TestHTTPResourceContextReturnsOriginalPayloadForExplicitCluster(t *testing.T) {
	_, handler := newTestHandler(t)
	selection := ResourceSelection{ClusterID: "east", Kind: "pods", Namespace: "default", Name: "api"}
	response := requestJSON(t, handler, http.MethodPost, "/api/resource-context", selection)
	if response.Code != http.StatusOK {
		t.Fatalf("resource context: status %d body %s", response.Code, response.Body)
	}
	var result ClusterContext
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(sampleCluster("east", result.ObservedAt).Documents[1].Data)
	got, _ := json.Marshal(result.Documents[0].Data)
	if result.ClusterID != "east" || len(result.Documents) != 1 || !bytes.Equal(got, want) {
		t.Fatalf("resource payload changed: %+v", result)
	}
}

func TestMCPFleetToolsUseTheSameSynchronizedModels(t *testing.T) {
	_, handler := newTestHandler(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "fleet-sync-test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: server.URL + "/mcp-fleet", DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	wantDescriptions := map[string]string{
		"list_clusters":        "List synchronized cluster observations and centrally configured multi-cloud groups. No live Kubernetes queries.",
		"get_agent_context":    "Read synchronized KubeAtlas context for exactly one clusterId or fleetId. Original payloads stay inside cluster envelopes. Membership is not proof of cross-cluster resource relationships. Resource detail is retrieved with get_resource_context.",
		"get_resource_context": "Read the original synchronized KubeAtlas get_resource result for one explicit clusterId, plural resource kind, API group, namespace and name. Inspect freshness and limitations; unavailable data is not evidence of absence.",
	}
	for _, tool := range tools.Tools {
		if want, exists := wantDescriptions[tool.Name]; exists {
			if tool.Description != want {
				t.Errorf("%s description = %q", tool.Name, tool.Description)
			}
			delete(wantDescriptions, tool.Name)
		}
	}
	if len(wantDescriptions) != 0 {
		t.Fatalf("missing MCP tools: %v", wantDescriptions)
	}

	call, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_agent_context", Arguments: map[string]any{"clusterId": "east"},
	})
	if err != nil || call.IsError || len(call.Content) != 1 {
		t.Fatalf("single-cluster tool result = %+v, err = %v", call, err)
	}
	content, ok := call.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("tool content type = %T", call.Content[0])
	}
	var context AgentContext
	if err := json.Unmarshal([]byte(content.Text), &context); err != nil {
		t.Fatal(err)
	}
	if len(context.Clusters) != 1 || len(context.Clusters[0].Documents) == 0 {
		t.Fatalf("single-cluster MCP context is incomplete: %+v", context)
	}
	expected := sampleCluster("east", context.Clusters[0].ObservedAt).Documents[0]
	if context.Mode != "single" || !reflect.DeepEqual(context.Clusters[0].Documents[0], expected) {
		t.Fatalf("canonical MCP payload changed: %+v", context)
	}

	ambiguous, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_agent_context", Arguments: map[string]any{"clusterId": "east", "fleetId": "prod"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ambiguous.IsError {
		t.Fatal("MCP accepted ambiguous cluster/fleet selection")
	}
	missing, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_resource_context", Arguments: map[string]any{"kind": "pods", "namespace": "default", "name": "api"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !missing.IsError {
		t.Fatal("MCP accepted resource detail without explicit clusterId")
	}
}
