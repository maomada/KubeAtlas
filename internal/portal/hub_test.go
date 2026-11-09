package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/cloud"
	"github.com/maomada/KubeAtlas/internal/hub"
	"github.com/maomada/KubeAtlas/internal/hubstate"
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
