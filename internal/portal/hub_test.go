package portal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/auth"
	"github.com/maomada/KubeAtlas/internal/cloud"
	"github.com/maomada/KubeAtlas/internal/hub"
	"github.com/maomada/KubeAtlas/internal/hubstate"
	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
)

func newPortalHubFixture(t *testing.T, factory bool) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	registry, err := OpenRegistry(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, "hub", "state.json")
	store, err := hubstate.OpenStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &HubConfig{Store: store, Groups: []string{"radar:idp:operators"}}
	if factory {
		cfg.MCPFactory = func(*hub.Hub, *hubstate.Store) (http.Handler, error) {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				principal, ok := hub.PrincipalFromContext(r.Context())
				if !ok || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("MCP boundary lacks principal or exposes credentials")
				}
				hubJSON(w, http.StatusOK, principal)
			}), nil
		}
	}
	handler, err := NewHandler(registry, Credentials{Username: testUsername, Password: testPassword, Hub: cfg}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handler.Close)
	return handler, statePath
}

func startPortalHubAgent(t *testing.T, handler *Handler, token, clusterID string, source http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	connected := make(chan struct{}, 1)
	done := make(chan error, 1)
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/agent"
	go func() {
		done <- cloud.Run(ctx, cloud.Config{
			URL: endpoint, Token: token, ClusterID: clusterID, ClusterName: clusterID,
			Handler: source, OnConnected: func() { connected <- struct{}{} },
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("Portal Hub Agent did not stop after cancellation")
		}
	})
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("Portal Hub Agent did not connect")
	}
	return server
}

func hubRequest(t *testing.T, h *Handler, cookie *http.Cookie, token, method, path, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("X-Forwarded-User", "forged")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: status=%d, want=%d", method, path, w.Code, status)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" {
		t.Fatal("Hub response missing cache or request identity protection")
	}
	return w
}

func decodeHubResponse[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPortalHubCredentialBoundaryAndLifecycle(t *testing.T) {
	h, statePath := newPortalHubFixture(t, true)
	cookie := loginForTest(t, h)
	agent := decodeHubResponse[hubstate.IssuedAgent](t, hubRequest(t, h, cookie, "", "POST", "/api/hub/clusters", `{"id":"alpha","name":"Alpha"}`, 201))
	hubRequest(t, h, nil, agent.Token, "POST", "/api/mcp/credentials", `{"clusterIds":["alpha"],"readOnly":false}`, 401)
	for _, body := range []string{
		`{"clusterIds":["alpha"]}`, `{"clusterIds":["alpha"],"readOnly":null}`,
		`{"clusterIds":["alpha"],"readOnly":true,"readOnly":false}`,
		`{"clusterIds":["alpha"],"ReadOnly":true}`, `{"clusterIds":["alpha"],"readOnly":true,"ownerId":"other"}`,
		`{"clusterIds":[],"readOnly":true}`, `{"clusterIds":["*"],"readOnly":true}`,
		`{"clusterIds":["missing"],"readOnly":true}`, `{} {}`, `null`, `[]`,
	} {
		hubRequest(t, h, cookie, "", "POST", "/api/mcp/credentials", body, 400)
	}
	issued := decodeHubResponse[hubstate.IssuedCredential](t, hubRequest(t, h, cookie, "", "POST", "/api/mcp/credentials", `{"label":"client","clusterIds":["alpha"],"readOnly":true,"ttlSeconds":900}`, 201))
	hubRequest(t, h, nil, issued.Token, "GET", "/api/hub/clusters", "", 401)
	hubRequest(t, h, cookie, issued.Token, "POST", "/api/mcp/credentials", `{"clusterIds":["alpha"],"readOnly":false}`, 401)
	hubRequest(t, h, cookie, "", "POST", "/mcp-fleet", `{}`, 401)
	hubRequest(t, h, cookie, "wrong", "POST", "/mcp-fleet", `{}`, 401)
	hubRequest(t, h, nil, agent.Token, "POST", "/mcp-fleet", `{}`, 401)
	principal := decodeHubResponse[hub.Principal](t, hubRequest(t, h, cookie, issued.Token, "POST", "/mcp-fleet", `{}`, 200))
	if principal.UserID != h.liveHub.userID || principal.CredentialID != issued.Credential.ID || !principal.ReadOnly || len(principal.ClusterIDs) != 1 || principal.ClusterIDs[0] != "alpha" || len(principal.Groups) != 1 || principal.Groups[0] != "radar:idp:operators" {
		t.Fatal("MCP principal differs from owner-bound credential")
	}
	listed := hubRequest(t, h, cookie, "", "GET", "/api/mcp/credentials", "", 200)
	disk, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(listed.Body.String(), issued.Token) || strings.Contains(listed.Body.String(), "digest") || strings.Contains(string(disk), issued.Token) || strings.Contains(string(disk), agent.Token) {
		t.Fatal("plaintext credential escaped its issuance response")
	}
	refreshed := decodeHubResponse[hubstate.IssuedCredential](t, hubRequest(t, h, nil, issued.Token, "POST", "/api/mcp/credentials/refresh", `{"ttlSeconds":900}`, 200))
	old := decodeHubResponse[map[string]any](t, hubRequest(t, h, cookie, issued.Token, "GET", "/api/mcp/credentials/current", "", 401))
	if old["error_code"] != "credential_revoked" || !refreshed.Credential.ReadOnly || refreshed.Credential.RefreshUntil != issued.Credential.RefreshUntil {
		t.Fatal("refresh did not preserve scope/family or revoke previous token")
	}
	hubRequest(t, h, nil, refreshed.Token, "POST", "/api/mcp/credentials/refresh", `{"readOnly":false}`, 400)
	hubRequest(t, h, cookie, "", "POST", "/api/mcp/credentials/"+refreshed.Credential.ID+"/revoke", `{}`, 204)
	hubRequest(t, h, nil, refreshed.Token, "POST", "/mcp-fleet", `{}`, 401)
	hubRequest(t, h, cookie, "", "POST", "/api/mcp/credentials/unknown/revoke", `{}`, 404)
}

func TestPortalBrowserAPIRequiresSessionAndExactOrigin(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	cookie := loginForTest(t, h)
	hubRequest(t, h, cookie, "", http.MethodPost, "/api/hub/clusters", `{"id":"alpha","name":"Alpha"}`, http.StatusCreated)

	request := func(path, origin, site, token string, withCookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Sec-Fetch-Site", site)
		if withCookie {
			r.AddCookie(cookie)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if got := request("/c/alpha/api/settings", "http://localhost", "same-origin", "", true); got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != `{"preferenceStorage":"browser"}` {
		t.Fatalf("browser settings response status=%d body=%q", got.Code, got.Body.String())
	}
	if got := request("/c/alpha/api/settings", "http://localhost", "same-origin", "token", true); got.Code != http.StatusUnauthorized {
		t.Fatalf("cookie plus bearer status=%d, want 401", got.Code)
	}
	if got := request("/c/alpha/api/settings", "http://attacker.invalid", "cross-site", "", true); got.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d, want 403", got.Code)
	}
	if got := request("/c/missing/api/settings", "http://localhost", "same-origin", "", true); got.Code != http.StatusNotFound {
		t.Fatalf("unknown cluster status=%d, want 404", got.Code)
	}
	if got := request("/c/alpha/api/settings", "http://localhost", "same-origin", "bearer-only", false); got.Code != http.StatusUnauthorized {
		t.Fatalf("bearer-only status=%d, want 401", got.Code)
	}
}

func TestPortalClusterAPIUsesSelectedClusterAndSanitizesIdentity(t *testing.T) {
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	h, _ := newPortalHubFixture(t, false)
	cookie := loginForTest(t, h)
	agent := decodeHubResponse[hubstate.IssuedAgent](t, hubRequest(t, h, cookie, "", http.MethodPost, "/api/hub/clusters", `{"id":"alpha","name":"Alpha"}`, http.StatusCreated))
	type capture struct {
		Method, Path, Query, User, Host, Auth, Cookie, Origin, Referer, FetchSite, Forwarded string
		Groups, Body                                                                         []string
		Tunnel                                                                               bool
	}
	requests := make(chan capture, 3)
	source := auth.Authenticate(auth.Config{Mode: "proxy", Secret: "portal-browser-test-secret"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		user := pkgauth.UserFromContext(r.Context())
		entry := capture{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Host: r.Host,
			Auth: r.Header.Get("Authorization"), Cookie: r.Header.Get("Cookie"),
			Origin: r.Header.Get("Origin"), Referer: r.Header.Get("Referer"),
			FetchSite: r.Header.Get("Sec-Fetch-Site"), Forwarded: r.Header.Get("X-Forwarded-User"),
			Tunnel: cloud.IsAuthenticatedTunnelRequest(r.Context()),
		}
		if user != nil {
			entry.User = user.Username
			entry.Groups = append([]string(nil), user.Groups...)
		}
		if len(body) > 0 {
			entry.Body = []string{string(body)}
		}
		requests <- entry
		if r.URL.Path == "/api/resources/uncertain" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"source":"live"}`)
	}))
	startPortalHubAgent(t, h, agent.Token, "alpha", source)

	doRequest := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		r.AddCookie(cookie)
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("Referer", "http://localhost/clusters")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("X-Forwarded-User", "forged-user")
		r.Header.Set("X-Forwarded-Groups", "radar:admin")
		r.Header.Set("X-Radar-Role", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	read := doRequest(http.MethodGet, "/c/alpha/api/resources/pods?namespace=demo-shop", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"source":"live"`) {
		t.Fatalf("REST read status=%d body=%q", read.Code, read.Body.String())
	}
	write := doRequest(http.MethodPost, "/c/alpha/api/resources/apply?namespace=demo-shop", `{"kind":"ConfigMap"}`)
	if write.Code != http.StatusNoContent {
		t.Fatalf("REST write status=%d body=%q", write.Code, write.Body.String())
	}
	uncertain := doRequest(http.MethodPost, "/c/alpha/api/resources/uncertain", `{"kind":"Deployment"}`)
	if uncertain.Code != http.StatusInternalServerError ||
		!strings.Contains(uncertain.Body.String(), `"outcome":"unknown"`) ||
		uncertain.Header().Get("X-Request-ID") == "" {
		t.Fatalf("source failure did not return a typed unknown outcome: status=%d body=%q", uncertain.Code, uncertain.Body.String())
	}
	for _, want := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/resources/pods"},
		{http.MethodPost, "/api/resources/apply"},
	} {
		got := <-requests
		if got.Method != want.method || got.Path != want.path || got.Query != "namespace=demo-shop" ||
			got.User != h.liveHub.userID || len(got.Groups) != 1 || got.Groups[0] != "radar:idp:operators" ||
			!got.Tunnel || got.Host != "localhost" || got.Auth != "" || got.Cookie != "" ||
			got.Origin != "" || got.Referer != "" || got.FetchSite != "" || got.Forwarded != h.liveHub.userID {
			t.Fatalf("source request identity/path was not sanitized: %+v", got)
		}
		if want.method == http.MethodPost && (len(got.Body) != 1 || got.Body[0] != `{"kind":"ConfigMap"}`) {
			t.Fatalf("REST write body was not preserved: %+v", got)
		}
	}
	if got := <-requests; got.Method != http.MethodPost || got.Path != "/api/resources/uncertain" {
		t.Fatalf("dispatched write was retried or path changed: %+v", got)
	}
	counts := doRequest(http.MethodGet, "/c/alpha/api/resource-counts?", "")
	if counts.Code != http.StatusOK {
		t.Fatalf("empty query status=%d body=%q", counts.Code, counts.Body.String())
	}
	if got := <-requests; got.Path != "/api/resource-counts" || got.Query != "" || got.User != h.liveHub.userID {
		t.Fatalf("empty query changed the scoped request: %+v", got)
	}
}

func TestPortalClusterAPIRedirectCannotReachHubManagement(t *testing.T) {
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	h, _ := newPortalHubFixture(t, false)
	cookie := loginForTest(t, h)
	agent := decodeHubResponse[hubstate.IssuedAgent](t, hubRequest(t, h, cookie, "", http.MethodPost, "/api/hub/clusters", `{"id":"alpha","name":"Alpha"}`, http.StatusCreated))
	type sourceRequest struct {
		method string
		path   string
		body   string
	}
	requests := make(chan sourceRequest, 2)
	source := auth.Authenticate(auth.Config{Mode: "proxy", Secret: "portal-redirect-test-secret"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- sourceRequest{method: r.Method, path: r.URL.Path, body: string(body)}
		w.Header().Set("Location", "/api/hub/clusters")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	server := startPortalHubAgent(t, h, agent.Token, "alpha", source)
	request, _ := http.NewRequest(
		http.MethodPost, server.URL+"/c/alpha/api/resources/apply",
		strings.NewReader(`{"id":"escaped","name":"Escaped"}`),
	)
	request.AddCookie(cookie)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", server.URL)
	request.Header.Set("Referer", server.URL+"/clusters")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway || response.Header.Get("Location") != "" {
		t.Fatalf("source redirect escaped Portal boundary: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	var payload struct {
		ErrorCode string `json:"error_code"`
		Message   string `json:"message"`
		RequestID string `json:"requestId"`
		Outcome   string `json:"outcome"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.ErrorCode != "source_redirect" || payload.Message == "" ||
		payload.RequestID == "" || payload.RequestID != response.Header.Get("X-Request-ID") ||
		payload.Outcome != "unknown" || payload.Retryable {
		t.Fatalf("source redirect error was not typed/conservative: %+v", payload)
	}
	select {
	case got := <-requests:
		if got.method != http.MethodPost || got.path != "/api/resources/apply" ||
			got.body != `{"id":"escaped","name":"Escaped"}` {
			t.Fatalf("unexpected source request: %+v", got)
		}
	default:
		t.Fatal("source mutation was not dispatched")
	}
	select {
	case got := <-requests:
		t.Fatalf("source request was repeated: %+v", got)
	default:
	}
	if _, exists := h.liveHub.store.Cluster("escaped"); exists {
		t.Fatal("source redirect reached Portal Hub cluster management")
	}
}

func TestBrowserResponseBodyLimitRejectsUnknownLength(t *testing.T) {
	if maxBrowserResponseBytes != 32<<20 {
		t.Fatalf("browser response limit=%d, want 32 MiB", maxBrowserResponseBytes)
	}
	response := &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader("12345")), ContentLength: -1,
	}
	failure := prepareBrowserResponse(response, http.MethodPost, false, 4)
	if failure == nil || failure.code != "source_response_too_large" || !failure.outcomeUnknown {
		t.Fatalf("unknown-length oversized mutation was not rejected: %+v", failure)
	}
	writer := httptest.NewRecorder()
	writer.Header().Set("X-Request-ID", "request-123")
	writeBrowserProxyFailure(writer, "request-123", failure)
	if writer.Code != http.StatusBadGateway || writer.Header().Get("Location") != "" ||
		writer.Header().Get("X-Request-ID") != "request-123" {
		t.Fatalf("oversized response error status/headers: status=%d headers=%v", writer.Code, writer.Header())
	}
	var payload struct {
		ErrorCode string `json:"error_code"`
		RequestID string `json:"requestId"`
		Outcome   string `json:"outcome"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(writer.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ErrorCode != "source_response_too_large" || payload.RequestID != "request-123" ||
		payload.Outcome != "unknown" || payload.Retryable || strings.Contains(writer.Body.String(), "12345") {
		t.Fatalf("oversized response was not replaced with a typed error: %+v body=%q", payload, writer.Body.String())
	}

	stream := &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader("data: ready\n\n")), ContentLength: -1,
	}
	if failure := prepareBrowserResponse(stream, http.MethodGet, true, 4); failure != nil {
		t.Fatalf("streaming response was buffered/rejected: %v", failure)
	}
	body, err := io.ReadAll(stream.Body)
	if err != nil || string(body) != "data: ready\n\n" {
		t.Fatalf("streaming response was consumed: body=%q err=%v", body, err)
	}
}

func TestPortalStreamLogoutReleasesHubSlots(t *testing.T) {
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	h, _ := newPortalHubFixture(t, false)
	cookie := loginForTest(t, h)
	agent := decodeHubResponse[hubstate.IssuedAgent](t, hubRequest(t, h, cookie, "", http.MethodPost, "/api/hub/clusters", `{"id":"alpha","name":"Alpha"}`, http.StatusCreated))
	const concurrentStreams = 32
	started := make(chan struct{}, concurrentStreams)
	exited := make(chan struct{}, concurrentStreams)
	source := auth.Authenticate(auth.Config{Mode: "proxy", Secret: "portal-stream-test-secret"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/events/stream" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"source":"live"}`)
			return
		}
		started <- struct{}{}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		exited <- struct{}{}
	}))
	server := startPortalHubAgent(t, h, agent.Token, "alpha", source)
	type streamResult struct {
		response *http.Response
		err      error
	}
	results := make(chan streamResult, concurrentStreams)
	for i := 0; i < concurrentStreams; i++ {
		request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/c/alpha/api/events/stream", nil)
		request.AddCookie(cookie)
		request.Header.Set("Origin", server.URL)
		request.Header.Set("Referer", server.URL+"/clusters")
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		go func() {
			response, err := server.Client().Do(request)
			if err == nil {
				event := make([]byte, len("data: ready\n\n"))
				_, err = io.ReadFull(response.Body, event)
				if err == nil && string(event) != "data: ready\n\n" {
					err = errors.New("unexpected SSE event")
				}
			}
			results <- streamResult{response: response, err: err}
		}()
	}
	streamResponses := make([]*http.Response, 0, concurrentStreams)
	for i := 0; i < concurrentStreams; i++ {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("Portal SSE request failed: %v", result.err)
			}
			streamResponses = append(streamResponses, result.response)
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d concurrent Portal streams became ready", len(streamResponses))
		}
	}
	for i := 0; i < concurrentStreams; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("source did not receive every Portal SSE stream")
		}
	}

	logout, _ := http.NewRequest(http.MethodPost, server.URL+"/api/auth/logout", strings.NewReader(`{}`))
	logout.AddCookie(cookie)
	logout.Header.Set("Content-Type", "application/json")
	logout.Header.Set("Origin", server.URL)
	logout.Header.Set("Sec-Fetch-Site", "same-origin")
	logoutResponse, err := server.Client().Do(logout)
	if err != nil {
		t.Fatal(err)
	}
	_ = logoutResponse.Body.Close()
	if logoutResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("Portal logout status=%d, want 204", logoutResponse.StatusCode)
	}
	for i := 0; i < concurrentStreams; i++ {
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Fatal("Portal logout did not cancel every upstream stream")
		}
	}

	newCookie := loginForTest(t, h)
	health, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(health, http.MethodGet, server.URL+"/c/alpha/api/health", nil)
	request.AddCookie(newCookie)
	request.Header.Set("Origin", server.URL)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("Hub stream slots were not released after logout: %v", err)
	}
	_ = response.Body.Close()
	for _, streamResponse := range streamResponses {
		_ = streamResponse.Body.Close()
	}
}

func TestPortalLogoutCancelsSessionBoundRequestContext(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	cookie := loginForTest(t, h)
	streamRequest := httptest.NewRequest(http.MethodGet, "http://localhost/c/alpha/api/events/stream", nil)
	streamRequest.AddCookie(cookie)
	ctx, cancel, ok := h.viewerSessionContext(streamRequest)
	if !ok {
		t.Fatal("valid Portal session did not produce a request context")
	}
	defer cancel()
	logout := httptest.NewRequest(http.MethodPost, "http://localhost/api/auth/logout", strings.NewReader(`{}`))
	logout.AddCookie(cookie)
	logout.Header.Set("Content-Type", "application/json")
	logout.Header.Set("Origin", "http://localhost")
	logout.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, logout)
	if response.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d, want 204", response.Code)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("logout did not cancel the active request context")
	}
}

func TestPortalSessionExpiryCancelsRequestContext(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	cookie := loginForTest(t, h)
	r := httptest.NewRequest(http.MethodGet, "http://localhost/c/alpha/api/events/stream", nil)
	r.AddCookie(cookie)
	ctx, cancel, ok := h.viewerSessionContext(r)
	if !ok {
		t.Fatal("valid Portal session did not produce a request context")
	}
	defer cancel()
	h.sessions.mu.Lock()
	h.sessions.now = func() time.Time { return time.Now().Add(sessionLifetime) }
	h.sessions.mu.Unlock()
	if h.hasViewerSession(r) {
		t.Fatal("expired Portal session was accepted")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("natural session expiry did not cancel the active request context")
	}
}

func TestHubPageRoutesAreExplicit(t *testing.T) {
	for _, path := range []string{
		"/investigations", "/install", "/settings/sessions", "/settings/security",
		"/settings/organization", "/settings/organization/members",
		"/settings/organization/agent-access", "/settings/organization/enrollment-keys",
		"/settings/organization/activity", "/settings/organization/issues",
		"/settings/organization/checks", "/settings/organization/sso",
		"/settings/organization/notifications", "/settings/organization/integrations",
		"/c/alpha", "/c/alpha/resources/pods",
	} {
		if !isHubPageRoute(httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)) {
			t.Errorf("known Hub page route %q was rejected", path)
		}
	}
	for _, path := range []string{
		"/arbitrary", "/settings/unknown", "/settings/organization/unknown",
		"/c/alpha/api/resources", "/c/alpha/assets/missing.js", "/c/../clusters",
	} {
		if isHubPageRoute(httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)) {
			t.Errorf("unknown or non-page route %q was accepted", path)
		}
	}
}

func TestPortalHubCredentialsSurviveSessionAndProcessRestart(t *testing.T) {
	h, statePath := newPortalHubFixture(t, true)
	cookie := loginForTest(t, h)
	hubRequest(t, h, cookie, "", "POST", "/api/hub/clusters", `{"id":"alpha","name":"Alpha"}`, 201)
	issued := decodeHubResponse[hubstate.IssuedCredential](t, hubRequest(t, h, cookie, "", "POST", "/api/mcp/credentials", `{"clusterIds":["alpha"],"readOnly":false}`, 201))
	h.Close()
	store, err := hubstate.OpenStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewHandler(h.registry, Credentials{Username: testUsername, Password: testPassword, Hub: &HubConfig{Store: store}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	hubRequest(t, restarted, cookie, "", "GET", "/api/mcp/credentials", "", 401)
	hubRequest(t, restarted, nil, issued.Token, "GET", "/api/mcp/credentials/current", "", 200)
	unavailable := decodeHubResponse[map[string]any](t, hubRequest(t, restarted, nil, issued.Token, "POST", "/mcp-fleet", `{}`, 503))
	if unavailable["error_code"] != "live_mcp_unavailable" {
		t.Fatal("missing live handler must not fall back to snapshots")
	}
	other, err := NewHandler(h.registry, Credentials{Username: "another-owner", Password: testPassword, Hub: &HubConfig{Store: store}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(other.Close)
	hubRequest(t, other, nil, issued.Token, "GET", "/api/mcp/credentials/current", "", 401)
}

func TestPortalHubRejectsAmbiguousCredentialsAndBrowserRequests(t *testing.T) {
	h, _ := newPortalHubFixture(t, true)
	cookie := loginForTest(t, h)
	hubRequest(t, h, cookie, "", "POST", "/api/hub/clusters", `{"id":"alpha","name":"Alpha"}`, 201)
	issued := decodeHubResponse[hubstate.IssuedCredential](t, hubRequest(t, h, cookie, "", "POST", "/api/mcp/credentials", `{"clusterIds":["alpha"],"readOnly":true}`, 201))
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{"duplicate bearer", func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+issued.Token) }, 401},
		{"unknown Host", func(r *http.Request) { r.Host = "attacker.example"; r.Header.Set("X-Forwarded-Host", "localhost") }, 403},
		{"foreign origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }, 403},
		{"duplicate origin", func(r *http.Request) { r.Header["Origin"] = []string{"http://localhost", "https://attacker.example"} }, 403},
		{"cross-site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"credential query", func(r *http.Request) { r.URL.RawQuery = "token=forbidden" }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://localhost/mcp-fleet", strings.NewReader(`{}`))
			r.Header.Set("Authorization", "Bearer "+issued.Token)
			r.AddCookie(cookie)
			tc.mutate(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d, want=%d", w.Code, tc.status)
			}
		})
	}
}

func TestPortalHubDirectoryRotationAndPersistenceFailure(t *testing.T) {
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	h, statePath := newPortalHubFixture(t, false)
	cookie := loginForTest(t, h)
	issued := decodeHubResponse[hubstate.IssuedAgent](t, hubRequest(t, h, cookie, "", "POST", "/api/hub/clusters", `{"id":"alpha","name":"Alpha"}`, 201))
	hubRequest(t, h, cookie, "", "POST", "/api/hub/groups", `{"id":"empty","name":"Empty","clusterIds":[]}`, 200)
	hubRequest(t, h, cookie, "", "PUT", "/api/hub/groups/empty", `{"id":"empty","name":"Named","clusterIds":["alpha"]}`, 200)
	hubRequest(t, h, cookie, "", "PUT", "/api/hub/clusters/alpha", `{"id":"beta","name":"Beta"}`, 400)
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- cloud.Run(ctx, cloud.Config{URL: "ws" + strings.TrimPrefix(server.URL, "http") + "/agent", Token: issued.Token, ClusterID: "alpha", ClusterName: "Alpha", Handler: http.NotFoundHandler()})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("source did not stop")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for len(h.liveHub.transport.Agents()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if agents := h.liveHub.transport.Agents(); len(agents) != 1 {
		t.Fatal("fixture source failed to connect")
	}
	directory := decodeHubResponse[struct {
		Clusters []hubDirectoryEntry `json:"clusters"`
	}](t, hubRequest(t, h, cookie, "", "GET", "/api/hub/clusters", "", 200))
	if len(directory.Clusters) != 1 || directory.Clusters[0].State != "connected" || directory.Clusters[0].Agent == nil {
		t.Fatal("live connection absent from directory")
	}
	if err := os.Chmod(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	hubRequest(t, h, cookie, "", "POST", "/api/hub/clusters/alpha/rotate", `{}`, 503)
	if len(h.liveHub.transport.Agents()) != 1 {
		t.Fatal("failed persistence disconnected an unchanged credential")
	}
	if _, err := h.liveHub.store.AuthenticateAgent(issued.Token, "alpha"); err != nil {
		t.Fatal("failed persistence changed credential")
	}
	if err := os.Chmod(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	rotated := decodeHubResponse[hubstate.IssuedAgent](t, hubRequest(t, h, cookie, "", "POST", "/api/hub/clusters/alpha/rotate", `{}`, 200))
	if len(h.liveHub.transport.Agents()) != 0 {
		t.Fatal("successful rotation left old session connected")
	}
	if _, err := h.liveHub.store.AuthenticateAgent(issued.Token, "alpha"); err == nil {
		t.Fatal("rotated credential still authenticates")
	}
	if _, err := h.liveHub.store.AuthenticateAgent(rotated.Token, "alpha"); err != nil {
		t.Fatal("new Agent credential rejected")
	}
	hubRequest(t, h, cookie, "", "POST", "/api/hub/clusters/alpha/revoke", `{}`, 204)
	directory = decodeHubResponse[struct {
		Clusters []hubDirectoryEntry `json:"clusters"`
	}](t, hubRequest(t, h, cookie, "", "GET", "/api/hub/clusters", "", 200))
	if directory.Clusters[0].State != "revoked" || directory.Clusters[0].Agent != nil {
		t.Fatal("revocation status incorrect")
	}
	hubRequest(t, h, cookie, "", "DELETE", "/api/hub/groups/empty", "", 204)
}
