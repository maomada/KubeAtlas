package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/maomada/KubeAtlas/internal/fleetsync"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeFleetExecutor struct {
	name      string
	isError   bool
	calls     atomic.Int32
	block     bool
	schema    map[string]any
	arguments chan map[string]any
}

func (e *fakeFleetExecutor) ListTools(context.Context) ([]*mcpsdk.Tool, error) {
	schema := e.schema
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	return []*mcpsdk.Tool{{
		Name: "inspect", Description: e.name, InputSchema: schema,
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true},
	}}, nil
}

func (e *fakeFleetExecutor) CallTool(ctx context.Context, _ string, arguments map[string]any) (*mcpsdk.CallToolResult, error) {
	e.calls.Add(1)
	if e.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if e.arguments != nil {
		e.arguments <- arguments
	}
	return &mcpsdk.CallToolResult{
		Content:           []mcpsdk.Content{&mcpsdk.TextContent{Text: e.name}},
		StructuredContent: map[string]any{"source": e.name},
		IsError:           e.isError,
	}, nil
}

func TestFleetBridgeMCPClientFederatesToolsThroughAuthenticatedPortal(t *testing.T) {
	h, endpoint, tokens := newAgentHubFixture(t, []string{"east", "west"})
	eastSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"region": map[string]any{"type": "string", "description": "East region"},
		},
		"required": []string{"region"},
	}
	westSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"shard": map[string]any{"type": "integer", "description": "West shard"},
		},
		"required": []string{"shard"},
	}
	east := &fakeFleetExecutor{name: "east-result", schema: eastSchema, arguments: make(chan map[string]any, 1)}
	west := &fakeFleetExecutor{name: "west-result", isError: true, schema: westSchema, arguments: make(chan map[string]any, 1)}
	startTestAgent(t, endpoint, "east", tokens["east"], fleetsync.SourceIDForBinding("east-binding"), east)
	startTestAgent(t, endpoint, "west", tokens["west"], fleetsync.SourceIDForBinding("west-binding"), west)
	waitForAgentState(t, h.agentHub, fleetsync.Selection{FleetID: "group"}, "connected")

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	viewerClient := &http.Client{Jar: jar}
	loginBody, err := json.Marshal(map[string]string{"username": testUsername, "password": testPassword})
	if err != nil {
		t.Fatal(err)
	}
	login, err := viewerClient.Post(endpoint+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal(err)
	}
	_ = login.Body.Close()
	if login.StatusCode != http.StatusNoContent {
		t.Fatalf("Portal viewer login status = %d", login.StatusCode)
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "fleet-portal-sdk-test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint: endpoint + "/mcp-fleet", HTTPClient: viewerClient, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	catalogResult, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "list_cluster_tools", Arguments: map[string]any{"fleetId": "group"},
	})
	if err != nil || catalogResult.IsError || len(catalogResult.Content) != 1 {
		t.Fatalf("list_cluster_tools result = %+v, err = %v", catalogResult, err)
	}
	catalogText, ok := catalogResult.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("list_cluster_tools content type = %T", catalogResult.Content[0])
	}
	var catalog []fleetsync.ClusterTools
	if err := json.Unmarshal([]byte(catalogText.Text), &catalog); err != nil {
		t.Fatalf("decode list_cluster_tools result: %v", err)
	}
	byCluster := make(map[string]fleetsync.ClusterTools, len(catalog))
	for _, item := range catalog {
		byCluster[item.ClusterID] = item
	}
	for clusterID, expectedSchema := range map[string]map[string]any{"east": eastSchema, "west": westSchema} {
		item, ok := byCluster[clusterID]
		if !ok || item.State != "connected" || len(item.Tools) != 1 || item.Tools[0].Name != "inspect" {
			t.Fatalf("%s catalog = %+v", clusterID, item)
		}
		gotSchema, err := json.Marshal(item.Tools[0].InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		wantSchema, err := json.Marshal(expectedSchema)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotSchema, wantSchema) {
			t.Fatalf("%s input schema = %s, want original %s", clusterID, gotSchema, wantSchema)
		}
	}

	for _, tc := range []struct {
		clusterID string
		argument  map[string]any
		executor  *fakeFleetExecutor
		wantText  string
		wantError bool
	}{
		{"east", map[string]any{"region": "east-1"}, east, "east-result", false},
		{"west", map[string]any{"shard": 7}, west, "west-result", true},
	} {
		result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
			Name: "call_cluster_tool",
			Arguments: map[string]any{
				"clusterId": tc.clusterID, "fleetId": "group", "tool": "inspect", "arguments": tc.argument,
			},
		})
		if err != nil || result == nil {
			t.Fatalf("%s call_cluster_tool result = %+v, err = %v", tc.clusterID, result, err)
		}
		if len(result.Content) != 1 {
			t.Fatalf("%s content = %+v", tc.clusterID, result.Content)
		}
		content, ok := result.Content[0].(*mcpsdk.TextContent)
		if !ok || content.Text != tc.wantText {
			t.Fatalf("%s content = %#v, want %q", tc.clusterID, result.Content[0], tc.wantText)
		}
		structured, ok := result.StructuredContent.(map[string]any)
		if !ok || structured["source"] != tc.wantText {
			t.Fatalf("%s structured content = %#v", tc.clusterID, result.StructuredContent)
		}
		if result.IsError != tc.wantError {
			t.Fatalf("%s IsError = %v, want %v", tc.clusterID, result.IsError, tc.wantError)
		}
		select {
		case gotArguments := <-tc.executor.arguments:
			gotJSON, _ := json.Marshal(gotArguments)
			wantJSON, _ := json.Marshal(tc.argument)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("%s source arguments = %s, want %s", tc.clusterID, gotJSON, wantJSON)
			}
		case <-ctx.Done():
			t.Fatalf("%s source did not receive tool arguments", tc.clusterID)
		}
	}
}

func TestFleetBridgeDispatchesExplicitSourceAndPreservesNativeResults(t *testing.T) {
	h, endpoint, tokens := newAgentHubFixture(t, []string{"east", "west"})
	east := &fakeFleetExecutor{name: "east-result"}
	west := &fakeFleetExecutor{name: "west-result", isError: true}
	startTestAgent(t, endpoint, "east", tokens["east"], fleetsync.SourceIDForBinding("east-binding"), east)
	startTestAgent(t, endpoint, "west", tokens["west"], fleetsync.SourceIDForBinding("west-binding"), west)
	waitForAgentState(t, h.agentHub, fleetsync.Selection{FleetID: "group"}, "connected")
	duplicateCancel, duplicateErrors := startTestAgent(t, endpoint, "east", tokens["east"], fleetsync.SourceIDForBinding("east-binding"), &fakeFleetExecutor{name: "duplicate"})
	waitForAgentError(t, duplicateErrors)
	duplicateCancel()

	catalog, err := h.agentHub.ListClusterTools(context.Background(), fleetsync.Selection{FleetID: "group"})
	if err != nil || len(catalog) != 2 || catalog[0].Tools[0].Description != "east-result" ||
		catalog[1].Tools[0].Description != "west-result" {
		t.Fatalf("distinct source catalogs = %+v, err = %v", catalog, err)
	}
	for _, clusterID := range []string{"east", "west"} {
		result, err := h.agentHub.CallClusterTool(context.Background(), fleetsync.ClusterToolCall{
			ClusterID: clusterID, FleetID: "group", Tool: "inspect", Arguments: map[string]any{},
		})
		if err != nil || result == nil {
			t.Fatalf("%s call result = %+v, err = %v", clusterID, result, err)
		}
		source := result.StructuredContent.(map[string]any)["source"]
		if source != clusterID+"-result" {
			t.Fatalf("%s call reached %v", clusterID, source)
		}
		if result.IsError != (clusterID == "west") {
			t.Fatalf("%s isError = %v", clusterID, result.IsError)
		}
	}
	if east.calls.Load() != 1 || west.calls.Load() != 1 {
		t.Fatalf("same-name dispatch counts east=%d west=%d", east.calls.Load(), west.calls.Load())
	}
	if _, err := h.agentHub.CallClusterTool(context.Background(), fleetsync.ClusterToolCall{
		ClusterID: "west", FleetID: "east-only", Tool: "inspect", Arguments: map[string]any{},
	}); err == nil {
		t.Fatal("call to a non-member of fleetId was accepted")
	}
}

func TestFleetChannelReportsUnknownOutcomeWithoutRetryAfterTimeoutOrDisconnect(t *testing.T) {
	h, endpoint, tokens := newAgentHubFixture(t, []string{"east"})
	executor := &fakeFleetExecutor{name: "blocked", block: true}
	cancel, _ := startTestAgent(t, endpoint, "east", tokens["east"], fleetsync.SourceIDForBinding("east-binding"), executor)
	waitForAgentState(t, h.agentHub, fleetsync.Selection{ClusterID: "east"}, "connected")
	agent := h.agentHub.connectionFor("east")
	agent.writeMu.Lock()
	callCtx, cancelCall := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, err := h.agentHub.CallClusterTool(callCtx, fleetsync.ClusterToolCall{
			ClusterID: "east", Tool: "inspect", Arguments: map[string]any{}, TimeoutSeconds: 10,
		})
		queued <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		agent.mu.Lock()
		pending := len(agent.pending)
		agent.mu.Unlock()
		if pending == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	agent.mu.Lock()
	pending := len(agent.pending)
	agent.mu.Unlock()
	if pending != 1 {
		agent.writeMu.Unlock()
		cancelCall()
		t.Fatal("queued call did not wait before dispatch")
	}
	cancelCall()
	agent.writeMu.Unlock()
	if err := <-queued; err != context.Canceled {
		t.Fatalf("queued cancellation error = %v, want context.Canceled", err)
	}
	if got := executor.calls.Load(); got != 0 {
		t.Fatalf("pre-dispatch cancellation executed %d calls", got)
	}

	result, err := h.agentHub.CallClusterTool(context.Background(), fleetsync.ClusterToolCall{
		ClusterID: "east", Tool: "inspect", Arguments: map[string]any{}, TimeoutSeconds: 1,
	})
	if err != nil || result == nil || !result.IsError ||
		result.StructuredContent.(map[string]any)["state"] != "outcome_unknown" {
		t.Fatalf("timed out call result = %+v, err = %v", result, err)
	}
	if got := executor.calls.Load(); got != 1 {
		t.Fatalf("timed out call executed %d times", got)
	}

	done := make(chan *mcpsdk.CallToolResult, 1)
	callErr := make(chan error, 1)
	go func() {
		value, callErrValue := h.agentHub.CallClusterTool(context.Background(), fleetsync.ClusterToolCall{
			ClusterID: "east", Tool: "inspect", Arguments: map[string]any{}, TimeoutSeconds: 10,
		})
		done <- value
		callErr <- callErrValue
	}()
	deadline = time.Now().Add(3 * time.Second)
	for executor.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if executor.calls.Load() != 2 {
		t.Fatal("second call was not dispatched")
	}
	cancel()
	select {
	case disconnectedResult := <-done:
		if err := <-callErr; err != nil || disconnectedResult == nil || !disconnectedResult.IsError ||
			disconnectedResult.StructuredContent.(map[string]any)["state"] != "outcome_unknown" {
			t.Fatalf("disconnected result = %+v, err = %v", disconnectedResult, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disconnected call did not return")
	}
	if got := executor.calls.Load(); got != 2 {
		t.Fatalf("disconnected call replayed; executions=%d", got)
	}
}

func TestFleetChannelRejectsWrongRegistrationTokenAndSourceIdentity(t *testing.T) {
	h, endpoint, tokens := newAgentHubFixture(t, []string{"east"})
	executor := &fakeFleetExecutor{name: "identity"}
	wrongToken, wrongTokenErrors := startTestAgent(t, endpoint, "east", strings.Repeat("x", 40), fleetsync.SourceIDForBinding("east-binding"), executor)
	waitForAgentError(t, wrongTokenErrors)
	wrongToken()

	wrongSource, wrongSourceErrors := startTestAgent(t, endpoint, "east", tokens["east"], fleetsync.SourceIDForBinding("other-binding"), executor)
	waitForAgentError(t, wrongSourceErrors)
	wrongSource()
	listed, err := h.agentHub.ListClusterTools(context.Background(), fleetsync.Selection{ClusterID: "east"})
	if err != nil || len(listed) != 1 || listed[0].State != "unavailable" {
		t.Fatalf("rejected source became available: %+v, err=%v", listed, err)
	}
	if got := executor.calls.Load(); got != 0 {
		t.Fatalf("rejected source executed %d calls", got)
	}
}

func TestFleetChannelOriginAndBearerBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		origin    string
		fetchSite string
		badToken  bool
		want      int
	}{
		{name: "outbound client without origin", want: http.StatusSwitchingProtocols},
		{name: "same origin", origin: "same", want: http.StatusSwitchingProtocols},
		{name: "foreign origin", origin: "https://attacker.invalid", want: http.StatusForbidden},
		{name: "opaque origin", origin: "null", want: http.StatusForbidden},
		{name: "malformed origin", origin: "://", want: http.StatusForbidden},
		{name: "cross site metadata", fetchSite: "cross-site", want: http.StatusForbidden},
		{name: "same origin without valid bearer", origin: "same", badToken: true, want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, endpoint, tokens := newAgentHubFixture(t, []string{"east"})
			headers := http.Header{}
			token := tokens["east"]
			if tc.badToken {
				token = "invalid"
			}
			headers.Set("Authorization", "Bearer "+token)
			if tc.origin == "same" {
				headers.Set("Origin", endpoint)
			} else if tc.origin != "" {
				headers.Set("Origin", tc.origin)
			}
			if tc.fetchSite != "" {
				headers.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
			conn, response, err := dialer.Dial("ws"+strings.TrimPrefix(endpoint, "http")+"/api/fleet/agents/east/channel", headers)
			if conn != nil {
				defer conn.Close()
			}
			if response == nil {
				t.Fatalf("missing handshake response: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.want {
				t.Fatalf("handshake status = %d, want %d; error = %v", response.StatusCode, tc.want, err)
			}
			if tc.want == http.StatusSwitchingProtocols && err != nil {
				t.Fatalf("allowed handshake failed: %v", err)
			}
		})
	}
}

func newAgentHubFixture(t *testing.T, ids []string) (*Handler, string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	store, err := fleetsync.OpenStore(filepath.Join(dir, "fleet", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		snapshot := fleetsync.Snapshot{
			Version: fleetsync.Version, ClusterID: id, Name: id,
			SourceID:   fleetsync.SourceIDForBinding(id + "-binding"),
			ObservedAt: time.Now().UTC(), Documents: []fleetsync.Document{}, Limitations: []string{},
		}
		if err := store.Ingest(snapshot); err != nil {
			t.Fatal(err)
		}
	}
	tokens := make(map[string]string, len(ids))
	for _, id := range ids {
		tokens[id] = id + "-registration-token-with-at-least-32-characters"
	}
	registry, err := OpenRegistry(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(registry, Credentials{
		Tokens: tokens, Username: testUsername, Password: testPassword, AllowInsecureCookies: true,
	}, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) > 1 {
		members := append([]string(nil), ids...)
		if err := store.PutGroup(fleetsync.Group{ID: "group", Name: "Group", ClusterIDs: members}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		handler.Close()
		server.Close()
		_ = store.Close()
	})
	return handler, server.URL, tokens
}

func startTestAgent(t *testing.T, endpoint, clusterID, token, sourceID string, executor *fakeFleetExecutor) (func(), <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	client, err := fleetsync.NewAgentClient(fleetsync.ReporterConfig{
		PortalURL: endpoint, Token: token, ClusterID: clusterID, Name: clusterID,
		Interval: 5 * time.Second, AllowHTTP: true,
	}, sourceID, executor)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	errorsSeen := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		client.Run(ctx, func(err error) {
			select {
			case errorsSeen <- err:
			default:
			}
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("source agent did not stop after cancellation")
		}
	})
	return cancel, errorsSeen
}

func waitForAgentState(t *testing.T, hub *AgentHub, selection fleetsync.Selection, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tools, err := hub.ListClusterTools(context.Background(), selection)
		if err == nil && len(tools) > 0 {
			ready := true
			for _, cluster := range tools {
				if cluster.State != state {
					ready = false
				}
			}
			if ready {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("source agents did not reach state %q", state)
}

func waitForAgentError(t *testing.T, errorsSeen <-chan error) {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	select {
	case err := <-errorsSeen:
		if err == nil || err.Error() != "Fleet agent channel unavailable" {
			t.Fatalf("agent error exposed details: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("rejected agent did not report a channel error")
	}
}
