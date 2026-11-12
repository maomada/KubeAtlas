package portal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func TestHubConnectionSettingsAuthorizationAndPolicySnapshot(t *testing.T) {
	store, err := hubstate.OpenStore(filepath.Join(t.TempDir(), "hub", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := newPortalHub(Credentials{Username: "local-owner", Hub: &HubConfig{Store: store}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.transport.Close()
	owner := hubstate.Member{Role: "owner", Status: "active"}
	request := httptest.NewRequest(http.MethodPut, "/api/hub/connections", strings.NewReader(`{"lanURL":"https://lan.example.test/","publicURL":""}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	p.manageConnections(response, request, owner)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"lanURL":"https://lan.example.test"`) || !strings.Contains(response.Body.String(), `"catalogAvailable":false`) {
		t.Fatalf("owner update status=%d body=%s", response.Code, response.Body.String())
	}
	old := p.policySnapshot()
	requestFor := func(host string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://localhost/api/hub/connections", nil)
		r.Host = host
		return r
	}
	if !old.allows(requestFor("lan.example.test")) {
		t.Fatal("configured exact authority was not included in the new policy snapshot")
	}

	member := hubstate.Member{Role: "member", Status: "active"}
	for _, body := range []string{`{"lanURL":"https://next.example.test","publicURL":""}`, `{"lanURL":"https://evil.test","publicURL":"","extra":true}`} {
		request := httptest.NewRequest(http.MethodPut, "/api/hub/connections", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		p.manageConnections(response, request, member)
		if response.Code != http.StatusForbidden {
			t.Fatalf("member update status=%d body=%s", response.Code, response.Body.String())
		}
	}

	request = httptest.NewRequest(http.MethodPut, "/api/hub/connections", strings.NewReader(`{"lanURL":"","publicURL":"https://public.example.test"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	p.manageConnections(response, request, hubstate.Member{Role: "admin", Status: "active"})
	if response.Code != http.StatusOK {
		t.Fatalf("administrator update status=%d body=%s", response.Code, response.Body.String())
	}
	newer := p.policySnapshot()
	if newer.allows(requestFor("lan.example.test")) || !newer.allows(requestFor("public.example.test")) {
		t.Fatal("new immutable policy snapshot did not replace the persisted profile authorities")
	}
	if !old.allows(requestFor("lan.example.test")) {
		t.Fatal("updating persisted settings mutated an existing request policy snapshot")
	}
}

func TestHubConnectionsPersistenceFailureIsUnavailableWithoutChangingState(t *testing.T) {
	h, statePath := newPortalHubFixture(t, false)
	stateDir := filepath.Dir(statePath)
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	cookie := loginForTest(t, h)
	response := hubRequest(t, h, cookie, "", http.MethodPut, "/api/hub/connections", `{"lanURL":"https://lan.example.test","publicURL":""}`, http.StatusServiceUnavailable)
	if !strings.Contains(response.Body.String(), `"error_code":"store_unavailable"`) {
		t.Fatalf("persistence error was misclassified: %s", response.Body.String())
	}
	if got := h.liveHub.store.Connections(); got != (hubstate.ConnectionSettings{}) {
		t.Fatalf("failed persistence changed in-memory settings: %+v", got)
	}
}

func TestHubConnectionsAuthorizationThroughHandler(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	owner := loginForTest(t, h)
	put := func(cookie *http.Cookie, want int) {
		t.Helper()
		hubRequest(t, h, cookie, "", http.MethodPut, "/api/hub/connections", `{"lanURL":"https://lan.example.test","publicURL":""}`, want)
	}
	get := func(cookie *http.Cookie, want int) {
		t.Helper()
		hubRequest(t, h, cookie, "", http.MethodGet, "/api/hub/connections", "", want)
	}
	get(owner, http.StatusOK)
	put(owner, http.StatusOK)

	newSession := func(id, role, status string) *http.Cookie {
		t.Helper()
		if _, _, err := h.liveHub.store.UpsertOIDCMember(id, "Test member"); err != nil {
			t.Fatal(err)
		}
		if role != "" {
			if _, _, err := h.liveHub.store.UpdateMember(id, hubstate.MemberInput{Role: role, Status: status}); err != nil {
				t.Fatal(err)
			}
		}
		recorder := httptest.NewRecorder()
		if !h.createSession(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil), id, time.Now().Add(time.Hour)) {
			t.Fatal("could not create member session")
		}
		return recorder.Result().Cookies()[0]
	}
	admin := newSession("oidc:"+strings.Repeat("a", 64), "admin", "active")
	member := newSession("oidc:"+strings.Repeat("b", 64), "member", "active")
	pending := newSession("oidc:"+strings.Repeat("c", 64), "", "")
	get(admin, http.StatusOK)
	put(admin, http.StatusOK)
	get(member, http.StatusOK)
	put(member, http.StatusForbidden)
	get(pending, http.StatusForbidden)
	put(pending, http.StatusForbidden)
}

func TestHubConnectionsRejectsExtraJSONFields(t *testing.T) {
	store, err := hubstate.OpenStore(filepath.Join(t.TempDir(), "hub", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := newPortalHub(Credentials{Username: "local-owner", Hub: &HubConfig{Store: store}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.transport.Close()
	request := httptest.NewRequest(http.MethodPut, "/api/hub/connections", strings.NewReader(`{"lanURL":"","publicURL":"","unexpected":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	p.manageConnections(response, request, hubstate.Member{Role: "owner"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("extra JSON field status=%d body=%s", response.Code, response.Body.String())
	}
}
