package portal

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func loginForTest(t *testing.T, h *Handler) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": testUsername, "password": testPassword})
	r := httptest.NewRequest(http.MethodPost, "http://localhost/api/auth/login", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || len(w.Result().Cookies()) != 1 {
		t.Fatalf("login status = %d, cookies = %d", w.Code, len(w.Result().Cookies()))
	}
	return w.Result().Cookies()[0]
}

func TestViewerSessionLifecycle(t *testing.T) {
	h, statePath := testHandler(t, filepath.Join(t.TempDir(), "hub", "state.json"))
	h.static = fstest.MapFS{"portal.html": &fstest.MapFile{Data: []byte("login shell")}}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.sessions.now = func() time.Time { return now }
	request := func(method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost"+path, nil)
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("WWW-Authenticate") != "" {
			t.Fatal("must not trigger browser Basic Auth prompt")
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("auth responses must not be cached")
		}
		return w
	}
	if w := request("GET", "/", nil); w.Code != 200 || w.Body.String() != "login shell" {
		t.Fatalf("public root: %d %q", w.Code, w.Body.String())
	}
	if w := request("GET", "/fleet", nil); w.Code != 404 {
		t.Fatalf("removed Fleet page status = %d, want 404", w.Code)
	}
	if w := request("GET", "/login", nil); w.Code != 200 || w.Body.String() != "login shell" {
		t.Fatalf("public login shell: %d", w.Code)
	}
	cookie := loginForTest(t, h)
	if cookie.Name != "__Host-radar_portal_session" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != int(sessionLifetime.Seconds()) {
		t.Fatalf("unsafe session cookie attributes")
	}
	if w := request("GET", "/api/hub", cookie); w.Code != 200 {
		t.Fatalf("authenticated list: %d", w.Code)
	}
	if w := request("GET", "/api/auth/session", cookie); w.Code != 204 {
		t.Fatalf("authenticated session: %d", w.Code)
	}
	if w := request("GET", "/", cookie); w.Code != 200 {
		t.Fatalf("authenticated overview: %d", w.Code)
	}
	if w := request("GET", "/fleet", cookie); w.Code != 404 {
		t.Fatalf("removed Fleet page status = %d, want 404", w.Code)
	}
	if w := request("GET", "/login", cookie); w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Fatalf("authenticated login: %d", w.Code)
	}
	if w := request("PUT", "/api/registrations/cluster-a", cookie); w.Code != 404 {
		t.Fatalf("removed registration route status = %d, want 404", w.Code)
	}
	if w := request("GET", "/api/auth/logout", cookie); w.Code != 405 {
		t.Fatalf("GET logout: %d", w.Code)
	}
	if w := request("POST", "/api/auth/logout", cookie); w.Code != 204 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("logout: %d", w.Code)
	}
	if w := request("GET", "/api/hub", cookie); w.Code != 401 {
		t.Fatalf("revoked cookie accepted: %d", w.Code)
	}

	cookie = loginForTest(t, h)
	now = now.Add(sessionLifetime)
	if w := request("GET", "/api/hub", cookie); w.Code != 401 {
		t.Fatalf("expired cookie accepted: %d", w.Code)
	}
	if len(h.sessions.expires) != 0 {
		t.Fatal("expired session not removed")
	}
	cookie = loginForTest(t, h)
	store, err := hubstate.OpenStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewHandler(Credentials{Username: testUsername, Password: testPassword, Hub: &HubConfig{Store: store}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	r := httptest.NewRequest("GET", "http://localhost/api/hub", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	restarted.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("session survived restart")
	}
}

func TestRegistrationCreatesPendingPasswordAccountWithoutMembership(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	register := func(email, password string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"email": email, "password": password})
		r := httptest.NewRequest(http.MethodPost, "http://localhost/api/auth/register", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	password := "pending-account-password"
	response := register("  Pending.User@Example.test ", password)
	if response.Code != http.StatusCreated || strings.Contains(response.Body.String(), password) || len(response.Result().Cookies()) != 0 || !strings.Contains(response.Body.String(), "pending") {
		t.Fatalf("registration status=%d body=%q cookies=%d", response.Code, response.Body.String(), len(response.Result().Cookies()))
	}
	account, ok := h.liveHub.store.AccountByEmail("pending.user@example.test")
	if !ok || account.Status != "pending" || !strings.HasPrefix(account.PasswordHash, "$2") || account.PasswordHash == password ||
		len(h.liveHub.store.OrganizationsForAccount(account.ID)) != 0 {
		t.Fatalf("registered account = %#v, ok=%v", account, ok)
	}
	if response := register("PENDING.USER@example.test", password); response.Code != http.StatusConflict || strings.Contains(response.Body.String(), password) {
		t.Fatalf("duplicate registration status=%d body=%q", response.Code, response.Body.String())
	}
	login := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"username": "PENDING.USER@example.test", "password": password})
		r := httptest.NewRequest(http.MethodPost, "http://localhost/api/auth/login", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	loginResponse := login()
	if loginResponse.Code != http.StatusNoContent || len(loginResponse.Result().Cookies()) != 1 {
		t.Fatalf("pending login status=%d body=%q", loginResponse.Code, loginResponse.Body.String())
	}
	cookie := loginResponse.Result().Cookies()[0]
	status := httptest.NewRequest(http.MethodGet, "http://localhost/api/hub", nil)
	status.AddCookie(cookie)
	statusResponse := httptest.NewRecorder()
	h.ServeHTTP(statusResponse, status)
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), "pending") || !strings.Contains(statusResponse.Body.String(), "activeOrganizationId") {
		t.Fatalf("pending Hub status=%d body=%s", statusResponse.Code, statusResponse.Body.String())
	}
	clusters := httptest.NewRequest(http.MethodGet, "http://localhost/api/hub/clusters", nil)
	clusters.AddCookie(cookie)
	clustersResponse := httptest.NewRecorder()
	h.ServeHTTP(clustersResponse, clusters)
	if clustersResponse.Code != http.StatusForbidden {
		t.Fatalf("pending directory status=%d body=%s", clustersResponse.Code, clustersResponse.Body.String())
	}
	if _, err := h.liveHub.store.UpdatePlatformUser(account.ID, "disabled", nil); err != nil {
		t.Fatal(err)
	}
	if loginResponse = login(); loginResponse.Code != http.StatusUnauthorized {
		t.Fatalf("disabled account login status=%d", loginResponse.Code)
	}
}

func TestLoginRejectsInvalidCredentialsAndCrossOriginMutations(t *testing.T) {
	valid, _ := json.Marshal(map[string]string{"username": testUsername, "password": testPassword})
	for _, tt := range []struct {
		name, method, path, body, contentType, origin, fetchSite string
		status                                                   int
	}{
		{"wrong password", "POST", "/api/auth/login", `{"username":"portal-viewer-user","password":"wrong"}`, "application/json", "", "", 401},
		{"wrong user", "POST", "/api/auth/login", `{"username":"wrong","password":"portal-viewer-password"}`, "application/json", "", "", 401},
		{"empty", "POST", "/api/auth/login", `{}`, "application/json", "", "", 401},
		{"malformed", "POST", "/api/auth/login", `{`, "application/json", "", "", 400},
		{"oversized", "POST", "/api/auth/login", strings.Repeat(" ", 17<<10), "application/json", "", "", 400},
		{"trailing JSON", "POST", "/api/auth/login", string(valid) + `{}`, "application/json", "", "", 400},
		{"unknown field", "POST", "/api/auth/login", `{"extra":true}`, "application/json", "", "", 400},
		{"GET login", "GET", "/api/auth/login", "", "", "", "", 405},
		{"form login", "POST", "/api/auth/login", string(valid), "text/plain", "", "", 415},
		{"cross-origin login", "POST", "/api/auth/login", string(valid), "application/json", "https://evil.test", "same-site", 403},
		{"empty origin", "POST", "/api/auth/login", string(valid), "application/json", "", "", 403},
		{"cross-site login", "POST", "/api/auth/login", string(valid), "application/json", "", "cross-site", 403},
		{"null origin", "POST", "/api/auth/login", string(valid), "application/json", "null", "", 403},
		{"cross-origin logout", "POST", "/api/auth/logout", `{}`, "application/json", "https://evil.test", "", 403},
		{"form logout", "POST", "/api/auth/logout", `{}`, "text/plain", "", "", 415},
		{"same-origin", "POST", "/api/auth/login", string(valid), "application/json", "https://localhost", "same-origin", 204},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := testHandler(t, filepath.Join(t.TempDir(), "hub-state.json"))
			r := httptest.NewRequest(tt.method, "https://localhost"+tt.path, strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)
			if tt.origin != "" || tt.name == "empty origin" {
				r.Header.Set("Origin", tt.origin)
			}
			if tt.fetchSite != "" {
				r.Header.Set("Sec-Fetch-Site", tt.fetchSite)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d", w.Code, tt.status)
			}
			if tt.status != 204 && len(w.Result().Cookies()) != 0 {
				t.Fatal("rejected request sets cookie")
			}
			if strings.Contains(w.Body.String(), testPassword) || w.Header().Get("WWW-Authenticate") != "" {
				t.Fatal("credential leak or Basic challenge")
			}
		})
	}
}

func TestViewerSessionRejectsBasicAndForgedCookies(t *testing.T) {
	h, _ := testHandler(t, filepath.Join(t.TempDir(), "hub-state.json"))
	r := httptest.NewRequest("GET", "http://localhost/api/hub", nil)
	r.SetBasicAuth(testUsername, testPassword)
	r.AddCookie(&http.Cookie{Name: h.cookieName(), Value: strings.Repeat("x", 43)})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("forged cookie or legacy Basic login accepted")
	}
	h.secureCookies = false
	cookie := loginForTest(t, h)
	if cookie.Secure || cookie.Name != "radar_portal_session" {
		t.Fatal("explicit local development cookie not configured")
	}
}

func TestLoginLimitsAndRotation(t *testing.T) {
	h, _ := testHandler(t, filepath.Join(t.TempDir(), "hub-state.json"))
	now := time.Now()
	h.sessions.now = func() time.Time { return now }
	cookie := loginForTest(t, h)
	body, _ := json.Marshal(map[string]string{"username": testUsername, "password": testPassword})
	r := httptest.NewRequest("POST", "http://localhost/api/auth/login", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || w.Result().Cookies()[0].Value == cookie.Value {
		t.Fatal("session was not rotated")
	}
	if _, ok := h.sessions.expires[sha256.Sum256([]byte(cookie.Value))]; ok {
		t.Fatal("old session remains valid")
	}
	for i := 0; i < 18; i++ {
		loginForTest(t, h)
	}
	r = httptest.NewRequest("POST", "http://localhost/api/auth/login", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("login attempts are not bounded")
	}
	now = now.Add(time.Minute)
	loginForTest(t, h)
	for i := len(h.sessions.expires); i < maxViewerSessions; i++ {
		h.sessions.expires[sha256.Sum256([]byte(time.Unix(int64(i), 0).String()))] = now.Add(sessionLifetime)
	}
	r = httptest.NewRequest("POST", "http://localhost/api/auth/login", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || len(h.sessions.expires) != maxViewerSessions {
		t.Fatal("session storage is not bounded")
	}
}
