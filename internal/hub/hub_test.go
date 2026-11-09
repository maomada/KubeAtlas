package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/maomada/KubeAtlas/internal/auth"
	"github.com/maomada/KubeAtlas/internal/cloud"
	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
)

type sourceObservation struct {
	ClusterID   string   `json:"clusterId"`
	UserID      string   `json:"userId"`
	Groups      []string `json:"groups"`
	Tunnel      bool     `json:"tunnel"`
	Auth        string   `json:"authorization"`
	Cookie      string   `json:"cookie"`
	Token       string   `json:"kubernetesToken"`
	Impersonate string   `json:"impersonateUser"`
	Host        string   `json:"host"`
	Protocol    string   `json:"mcpProtocol"`
}

func newTestHub(t *testing.T) (*Hub, *httptest.Server) {
	t.Helper()
	identities := map[string]AgentIdentity{
		"token-alpha": {ClusterID: "alpha", Name: "Alpha cluster"},
		"token-beta":  {ClusterID: "beta", Name: "Beta cluster"},
	}
	hub, err := New(Config{AuthenticateAgent: func(_ context.Context, token, claimedID string) (AgentIdentity, error) {
		identity, ok := identities[token]
		if !ok {
			return AgentIdentity{}, errors.New("unknown token")
		}
		return identity, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(hub)
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = hub.Close() })
	return hub, server
}

func startSource(t *testing.T, endpoint, clusterID, token string, handler http.Handler) (<-chan struct{}, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	connected := make(chan struct{}, 8)
	done := make(chan error, 1)
	go func() {
		done <- cloud.Run(ctx, cloud.Config{
			URL: endpoint + "/agent", Token: token, ClusterID: clusterID,
			ClusterName: clusterID + " display name", Handler: handler,
			OnConnected: func() { connected <- struct{}{} },
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("cloud.Run did not stop after cancellation")
		}
	})
	return connected, done
}

func cloudWebSocketEndpoint(serverURL string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http")
}

func waitForAgent(t *testing.T, hub *Hub, clusterID string) Agent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, agent := range hub.Agents() {
			if agent.ClusterID == clusterID {
				return agent
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("agent %q did not connect", clusterID)
	return Agent{}
}

func testAuthHandler(handler http.Handler) http.Handler {
	return auth.Authenticate(auth.Config{Mode: "proxy", Secret: "hub-test-auth-secret"})(handler)
}

func TestHubTunnelRoutesMultipleSourcesAndIsolatesCallerIdentity(t *testing.T) {
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	hub, server := newTestHub(t)
	endpoint := cloudWebSocketEndpoint(server.URL)

	source := func(clusterID string) http.Handler {
		return testAuthHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := pkgauth.UserFromContext(r.Context())
			observation := sourceObservation{
				ClusterID: clusterID, Tunnel: cloud.IsAuthenticatedTunnelRequest(r.Context()),
				Auth: r.Header.Get("Authorization"), Cookie: r.Header.Get("Cookie"),
				Token:       r.Header.Get("X-Forwarded-Kubernetes-Token"),
				Impersonate: r.Header.Get("Impersonate-User"),
				Host:        r.Host, Protocol: r.Header.Get("MCP-Protocol-Version"),
			}
			if user != nil {
				observation.UserID = user.Username
				observation.Groups = append([]string(nil), user.Groups...)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Add("Set-Cookie", "source=session")
			_ = json.NewEncoder(w).Encode(observation)
		}))
	}
	startSource(t, endpoint, "alpha", "token-alpha", source("alpha"))
	startSource(t, endpoint, "beta", "token-beta", source("beta"))
	alpha := waitForAgent(t, hub, "alpha")
	beta := waitForAgent(t, hub, "beta")
	if alpha.ConnectionID == beta.ConnectionID || alpha.Name != "Alpha cluster" || beta.Name != "Beta cluster" {
		t.Fatalf("source identities were not kept distinct: alpha=%+v beta=%+v", alpha, beta)
	}
	if len(hub.Agents()) != 2 {
		t.Fatalf("active agents = %d, want 2", len(hub.Agents()))
	}

	principals := []Principal{
		{UserID: "user-one", Groups: []string{"radar:viewer"}, ClusterIDs: []string{"alpha"}},
		{UserID: "user-two", Groups: []string{"radar:member"}, ClusterIDs: []string{"alpha"}},
		{UserID: "user-three", Groups: []string{"radar:owner"}, ClusterIDs: []string{"beta"}},
	}
	var wg sync.WaitGroup
	failures := make(chan error, 24)
	for i := 0; i < 24; i++ {
		principal := principals[i%len(principals)]
		clusterID := principal.ClusterIDs[0]
		client, err := hub.HTTPClient(clusterID, principal)
		if err != nil {
			t.Fatalf("HTTPClient(%s): %v", clusterID, err)
		}
		wg.Add(1)
		go func(client *http.Client, principal Principal, clusterID string) {
			defer wg.Done()
			request, err := http.NewRequest(http.MethodPost, "http://localhost/mcp?mode=streamable", bytes.NewBufferString(`{"method":"tools/list"}`))
			if err != nil {
				failures <- err
				return
			}
			request.Host = "forged.example"
			request.Header.Set("Authorization", "Bearer caller-token")
			request.Header.Set("Cookie", "radar_session=forged")
			request.Header.Set("Forwarded", "for=attacker")
			request.Header.Set("X-Forwarded-User", "forged-user")
			request.Header.Set("X-Forwarded-Groups", "radar:owner")
			request.Header.Set("X-Forwarded-Kubernetes-Token", "caller-k8s-token")
			request.Header.Set("Impersonate-User", "system:masters")
			request.Header.Set("MCP-Protocol-Version", "2025-03-26")
			response, err := client.Do(request)
			if err != nil {
				failures <- err
				return
			}
			defer response.Body.Close()
			var result sourceObservation
			if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
				failures <- err
				return
			}
			wantCluster := clusterID
			if result.ClusterID != wantCluster || result.UserID != principal.UserID ||
				!equalStrings(result.Groups, principal.Groups) || !result.Tunnel ||
				result.Auth != "" || result.Cookie != "" || result.Token != "" ||
				result.Impersonate != "" || result.Host != "localhost" ||
				result.Protocol != "2025-03-26" || response.Header.Get("Set-Cookie") != "" {
				failures <- fmt.Errorf("source observed a mismatched caller or unsafe header: %+v response=%d", result, response.StatusCode)
			}
		}(client, principal, clusterID)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestHubHandshakeRejectsUntrustedClaimsBeforeTrust(t *testing.T) {
	var authentications atomic.Int32
	hub, err := New(Config{AuthenticateAgent: func(_ context.Context, token, claimed string) (AgentIdentity, error) {
		authentications.Add(1)
		switch token {
		case "token-alpha":
			return AgentIdentity{ClusterID: "alpha", Name: "Alpha"}, nil
		case "token-beta":
			return AgentIdentity{ClusterID: "beta", Name: "Beta"}, nil
		default:
			return AgentIdentity{}, errors.New("rejected")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(hub)
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = hub.Close() })
	endpoint := cloudWebSocketEndpoint(server.URL)

	t.Run("wrong path", func(t *testing.T) {
		response, err := http.Get(server.URL + "/elsewhere")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.StatusCode)
		}
	})
	t.Run("wrong method", func(t *testing.T) {
		response, err := http.Post(server.URL+"/agent", "text/plain", nil)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", response.StatusCode)
		}
	})

	cases := []struct {
		name         string
		query        string
		token        string
		credentials  string
		origin       string
		originHeader bool
		wantStatus   int
		wantAuths    int32
	}{
		{name: "missing bearer", query: "cluster_id=alpha", credentials: "impersonate", wantStatus: http.StatusUnauthorized, wantAuths: 0},
		{name: "bad bearer", query: "cluster_id=alpha", token: "invalid-token", credentials: "impersonate", wantStatus: http.StatusUnauthorized, wantAuths: 1},
		{name: "claimed id mismatch", query: "cluster_id=alpha", token: "token-beta", credentials: "impersonate", wantStatus: http.StatusUnauthorized, wantAuths: 2},
		{name: "missing identity", query: "", token: "token-alpha", credentials: "impersonate", wantStatus: http.StatusBadRequest, wantAuths: 2},
		{name: "user token mode", query: "cluster_id=alpha", token: "token-alpha", credentials: "user-token", wantStatus: http.StatusForbidden, wantAuths: 2},
		{name: "missing credentials mode", query: "cluster_id=alpha", token: "token-alpha", wantStatus: http.StatusForbidden, wantAuths: 2},
		{name: "browser origin", query: "cluster_id=alpha", token: "token-alpha", credentials: "impersonate", origin: "https://attacker.example", originHeader: true, wantStatus: http.StatusForbidden, wantAuths: 2},
		{name: "empty origin header", query: "cluster_id=alpha", token: "token-alpha", credentials: "impersonate", originHeader: true, wantStatus: http.StatusForbidden, wantAuths: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{}
			if tc.token != "" {
				headers.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.credentials != "" {
				headers.Set("X-Radar-Kubernetes-Credentials", tc.credentials)
			}
			if tc.originHeader {
				headers.Set("Origin", tc.origin)
			}
			target := endpoint + "/agent"
			if tc.query != "" {
				target += "?" + tc.query
			}
			conn, response, err := (&websocket.Dialer{}).Dial(target, headers)
			if conn != nil {
				conn.Close()
			}
			if response == nil {
				t.Fatalf("handshake response is nil, error = %v", err)
			}
			response.Body.Close()
			if response.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d (error %v)", response.StatusCode, tc.wantStatus, err)
			}
			if got := authentications.Load(); got != tc.wantAuths {
				t.Fatalf("authenticator calls = %d, want %d", got, tc.wantAuths)
			}
		})
	}
}

func TestHubRejectsDuplicateAndReleasesIDAfterDisconnect(t *testing.T) {
	hub, server := newTestHub(t)
	endpoint := cloudWebSocketEndpoint(server.URL)
	handler := testAuthHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	connected, _ := startSource(t, endpoint, "alpha", "token-alpha", handler)
	first := waitForAgent(t, hub, "alpha")
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("source did not complete the WebSocket handshake")
	}

	headers := http.Header{"Authorization": []string{"Bearer token-alpha"}, "X-Radar-Kubernetes-Credentials": []string{"impersonate"}}
	target := endpoint + "/agent?cluster_id=alpha&cluster_name=duplicate"
	conn, response, err := (&websocket.Dialer{}).Dial(target, headers)
	if conn != nil {
		conn.Close()
	}
	if response == nil {
		t.Fatalf("duplicate handshake response is nil: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate handshake status = %d, want 409", response.StatusCode)
	}
	if got := waitForAgent(t, hub, "alpha"); got.ConnectionID != first.ConnectionID {
		t.Fatalf("duplicate connection replaced active agent: before=%s after=%s", first.ConnectionID, got.ConnectionID)
	}

	hub.Disconnect("alpha")
	waitForNoAgent(t, hub, "alpha")
	second := waitForDifferentAgent(t, hub, "alpha", first.ConnectionID)
	if second.ConnectionID == first.ConnectionID {
		t.Fatal("reconnected agent reused the previous connection generation")
	}
}

func TestHubEnforcesScopeReadOnlyPathAndCancellation(t *testing.T) {
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	hub, server := newTestHub(t)
	endpoint := cloudWebSocketEndpoint(server.URL)
	started := make(chan struct{}, 1)
	sourceDone := make(chan struct{}, 1)
	releaseSource := make(chan struct{})
	handler := testAuthHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp-readonly" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/mcp" {
			close(started)
			select {
			case <-r.Context().Done():
			case <-releaseSource:
			}
			sourceDone <- struct{}{}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	startSource(t, endpoint, "alpha", "token-alpha", handler)
	agent := waitForAgent(t, hub, "alpha")

	if _, err := hub.HTTPClient("beta", Principal{UserID: "user", Groups: []string{"radar:viewer"}, ClusterIDs: []string{"alpha"}}); err == nil {
		t.Fatal("client creation outside principal cluster scope succeeded")
	}
	readOnlyClient, err := hub.HTTPClient("alpha", Principal{UserID: "reader", Groups: []string{"radar:viewer"}, ClusterIDs: []string{"alpha"}, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target string
		code   ErrorCode
	}{
		{target: "http://localhost/mcp", code: CodePermissionDenied},
		{target: "http://example.com/mcp-readonly", code: CodeInvalidParameters},
		{target: "http://localhost/api/connection", code: CodePermissionDenied},
	} {
		request, _ := http.NewRequest(http.MethodPost, tc.target, nil)
		_, err := readOnlyClient.Do(request)
		var tunnelErr *Error
		if !errors.As(err, &tunnelErr) || tunnelErr.Code != tc.code {
			t.Errorf("request to %s error = %v, want %s", tc.target, err, tc.code)
		}
	}
	readRequest, _ := http.NewRequest(http.MethodPost, "http://localhost/mcp-readonly", nil)
	readResponse, err := readOnlyClient.Do(readRequest)
	if err != nil {
		t.Fatalf("read-only request failed: %v", err)
	}
	readResponse.Body.Close()

	client, err := hub.HTTPClient("alpha", Principal{UserID: "writer", Groups: []string{"radar:member"}, ClusterIDs: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	requestContext, cancel := context.WithCancel(t.Context())
	slowRequest, _ := http.NewRequestWithContext(requestContext, http.MethodPost, "http://localhost/mcp", strings.NewReader("{}"))
	result := make(chan error, 1)
	go func() {
		response, requestErr := client.Do(slowRequest)
		if response != nil {
			response.Body.Close()
		}
		result <- requestErr
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("source did not receive the slow request")
	}
	cancel()
	select {
	case err := <-result:
		var tunnelErr *Error
		if !errors.As(err, &tunnelErr) {
			t.Fatalf("cancel error = %v, want typed tunnel error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled request did not return")
	}
	waitForStreamSlots(t, hub, agent.ClusterID, 0)
	close(releaseSource)
	select {
	case <-sourceDone:
	case <-time.After(2 * time.Second):
		t.Fatal("source handler did not finish after stream cancellation")
	}
	if got := waitForAgent(t, hub, "alpha"); got.ConnectionID != agent.ConnectionID {
		t.Fatal("request cancellation closed or replaced the unrelated agent session")
	}
}

func TestHubPinnedClientDoesNotReplayAfterReconnect(t *testing.T) {
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	hub, server := newTestHub(t)
	endpoint := cloudWebSocketEndpoint(server.URL)
	entered := make(chan struct{}, 2)
	counter := new(atomic.Int32)
	sourceHandler := testAuthHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := counter.Add(1)
		if count == 1 {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	startSource(t, endpoint, "alpha", "token-alpha", sourceHandler)
	first := waitForAgent(t, hub, "alpha")
	oldClient, err := hub.HTTPClient("alpha", Principal{UserID: "writer", Groups: []string{"radar:member"}, ClusterIDs: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	oldRequest, _ := http.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader("{}"))
	firstResult := make(chan error, 1)
	go func() {
		response, requestErr := oldClient.Do(oldRequest)
		if response != nil {
			response.Body.Close()
		}
		firstResult <- requestErr
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("initial request did not reach source")
	}
	hub.Disconnect("alpha")
	select {
	case <-firstResult:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnected source request did not return")
	}
	second := waitForDifferentAgent(t, hub, "alpha", first.ConnectionID)
	before := counter.Load()
	request, _ := http.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader("{}"))
	_, err = oldClient.Do(request)
	var tunnelErr *Error
	if !errors.As(err, &tunnelErr) || tunnelErr.Code != CodeSourceChanged {
		t.Fatalf("old-generation client error = %v, want source_changed", err)
	}
	if got := counter.Load(); got != before {
		t.Fatalf("old client replayed on new generation: source calls %d -> %d", before, got)
	}
	newClient, err := hub.HTTPClient("alpha", Principal{UserID: "writer", Groups: []string{"radar:member"}, ClusterIDs: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	newRequest, _ := http.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader("{}"))
	response, err := newClient.Do(newRequest)
	if err != nil {
		t.Fatalf("new generation request failed: %v", err)
	}
	response.Body.Close()
	if got := waitForAgent(t, hub, "alpha"); got.ConnectionID != second.ConnectionID {
		t.Fatal("new connection generation changed unexpectedly")
	}
}

func TestHubLimitsAgentsAndConcurrentStreams(t *testing.T) {
	hub, err := New(Config{AuthenticateAgent: func(context.Context, string, string) (AgentIdentity, error) {
		return AgentIdentity{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxAgents; i++ {
		clusterID := fmt.Sprintf("cluster-%03d", i)
		if err := hub.reserve(&agentState{agent: Agent{ClusterID: clusterID}, slots: make(chan struct{}, maxAgentStreams)}); err != nil {
			t.Fatalf("reserve agent %d: %v", i, err)
		}
	}
	if err := hub.reserve(&agentState{agent: Agent{ClusterID: "cluster-overflow"}, slots: make(chan struct{}, maxAgentStreams)}); err == nil {
		t.Fatal("hub accepted more than the active-agent limit")
	}
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	if err := hub.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	tunnelHub, server := newTestHub(t)
	endpoint := cloudWebSocketEndpoint(server.URL)
	started := make(chan struct{}, maxAgentStreams+1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseSources := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseSources()
	handler := testAuthHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	startSource(t, endpoint, "alpha", "token-alpha", handler)
	waitForAgent(t, tunnelHub, "alpha")
	client, err := tunnelHub.HTTPClient("alpha", Principal{UserID: "writer", Groups: []string{"radar:member"}, ClusterIDs: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}

	const requestCount = maxAgentStreams + 1
	cancels := make([]context.CancelFunc, requestCount)
	results := make(chan error, requestCount)
	for i := 0; i < maxAgentStreams; i++ {
		ctx, cancel := context.WithCancel(t.Context())
		cancels[i] = cancel
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/mcp", strings.NewReader("{}"))
		go func() {
			response, requestErr := client.Do(request)
			if response != nil {
				response.Body.Close()
			}
			results <- requestErr
		}()
	}
	for i := 0; i < maxAgentStreams; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("source did not receive the expected concurrent requests")
		}
	}
	if got := len(agentStateFor(t, tunnelHub, "alpha").slots); got != maxAgentStreams {
		t.Fatalf("open stream slots = %d, want %d", got, maxAgentStreams)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancels[maxAgentStreams] = cancel
	lastRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/mcp", strings.NewReader("{}"))
	go func() {
		response, requestErr := client.Do(lastRequest)
		if response != nil {
			response.Body.Close()
		}
		results <- requestErr
	}()
	select {
	case <-started:
		t.Fatal("hub dispatched a request beyond the per-agent stream limit")
	case <-time.After(100 * time.Millisecond):
	}

	cancels[0]()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("waiting request did not acquire the released stream slot")
	}
	for _, stop := range cancels {
		stop()
	}
	releaseSources()
	for i := 0; i < requestCount; i++ {
		select {
		case <-results:
		case <-time.After(3 * time.Second):
			t.Fatal("cancelled request did not finish")
		}
	}
	waitForStreamSlots(t, tunnelHub, "alpha", 0)
}

func waitForNoAgent(t *testing.T, hub *Hub, clusterID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := findAgent(hub, clusterID); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("agent %q is still active", clusterID)
}

func agentStateFor(t *testing.T, hub *Hub, clusterID string) *agentState {
	t.Helper()
	hub.mu.RLock()
	state := hub.agents[clusterID]
	hub.mu.RUnlock()
	if state == nil {
		t.Fatalf("agent %q is missing", clusterID)
	}
	return state
}

func waitForDifferentAgent(t *testing.T, hub *Hub, clusterID, oldID string) Agent {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		agent, ok := findAgent(hub, clusterID)
		if ok && agent.ConnectionID != oldID {
			return agent
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("agent %q did not reconnect with a new generation", clusterID)
	return Agent{}
}

func findAgent(hub *Hub, clusterID string) (Agent, bool) {
	for _, agent := range hub.Agents() {
		if agent.ClusterID == clusterID {
			return agent, true
		}
	}
	return Agent{}, false
}

func waitForStreamSlots(t *testing.T, hub *Hub, clusterID string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		state := hub.agents[clusterID]
		hub.mu.RUnlock()
		if state != nil && len(state.slots) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("agent stream slots did not return to %d", want)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestHubRejectsIdentityHeaderAndPathVariants(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("hub without an authenticator was accepted")
	}
	if !validClusterID("cluster-01") || validClusterID("Cluster-01") || validClusterID("cluster_01") {
		t.Fatal("cluster ID validation did not require lowercase DNS-like IDs")
	}
	for _, value := range []string{"", "system:masters", "bad\nuser"} {
		if principalAllowed(Principal{UserID: value, Groups: []string{"radar:viewer"}, ClusterIDs: []string{"alpha"}}, "alpha") {
			t.Errorf("accepted invalid user identity %q", value)
		}
	}
	for _, target := range []string{
		"https://localhost/mcp",
		"http://localhost.evil/mcp",
		"http://localhost:80/mcp",
		"http://user@localhost/mcp",
		"http://localhost/mcp#fragment",
		"http://localhost/mcp/extra",
	} {
		request, err := http.NewRequest(http.MethodPost, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		if validateMCPRequest(request, false) == nil {
			t.Errorf("accepted endpoint %q", target)
		}
	}
	upgradeRequest, _ := http.NewRequest(http.MethodGet, "http://localhost/mcp", nil)
	upgradeRequest.Header.Set("Upgrade", "websocket")
	if validateMCPRequest(upgradeRequest, false) == nil {
		t.Fatal("accepted WebSocket upgrade")
	}
}

func TestOldConnectionCleanupCannotRemoveNewGeneration(t *testing.T) {
	hub, err := New(Config{AuthenticateAgent: func(context.Context, string, string) (AgentIdentity, error) {
		return AgentIdentity{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	oldState := &agentState{agent: Agent{ClusterID: "alpha", ConnectionID: "old"}, slots: make(chan struct{}, maxAgentStreams)}
	if err := hub.reserve(oldState); err != nil {
		t.Fatal(err)
	}
	hub.remove(oldState)
	newState := &agentState{agent: Agent{ClusterID: "alpha", ConnectionID: "new"}, slots: make(chan struct{}, maxAgentStreams)}
	if err := hub.reserve(newState); err != nil {
		t.Fatal(err)
	}
	hub.remove(oldState)
	hub.mu.RLock()
	current := hub.agents["alpha"]
	hub.mu.RUnlock()
	if current != newState {
		t.Fatal("stale connection cleanup removed the replacement generation")
	}
	_ = hub.Close()
}
