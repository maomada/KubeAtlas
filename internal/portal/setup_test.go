package portal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func setupRequest(t *testing.T, handler http.Handler, method, path string, body map[string]string, status int) *httptest.ResponseRecorder {
	t.Helper()
	const base = "http://127.0.0.1:1333"
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, base+path, bytes.NewReader(data))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", base)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, response.Code, status, response.Body)
	}
	return response
}

func setupStore(t *testing.T, path string) *hubstate.Store {
	t.Helper()
	store, err := hubstate.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestFirstRunSetupAndPersistedLogin(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "hub", "state.json")
	store := setupStore(t, statePath)
	assets := fstest.MapFS{"portal.html": {Data: []byte("portal app")}, "assets/portal.js": {Data: []byte("portal js")}}
	h, err := NewStartupHandler(Credentials{Hub: &HubConfig{Store: store}}, assets, statePath, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	if got := setupRequest(t, h, "GET", "/", nil, 303).Header().Get("Location"); got != "/setup" {
		t.Fatalf("first visit redirected to %q", got)
	}
	setupRequest(t, h, "GET", "/setup", nil, 200)
	fromLink := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1333/setup", nil)
	fromLink.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, fromLink)
	if response.Code != http.StatusOK {
		t.Fatalf("following a link to the setup page returned %d", response.Code)
	}
	setupRequest(t, h, "GET", "/assets/portal.js", nil, 200)
	setupRequest(t, h, "GET", "/healthz", nil, 204)
	setupRequest(t, h, "GET", "/agent", nil, 503)
	setupRequest(t, h, "POST", "/mcp", nil, 503)
	status := setupRequest(t, h, "GET", "/api/setup", nil, 200)
	code, err := ReadSetupCode(statePath)
	if err != nil || !strings.Contains(status.Body.String(), `"required":true`) || strings.Contains(status.Body.String(), code) {
		t.Fatal("private setup code unavailable locally or exposed in the setup status")
	}
	login := map[string]string{"username": "local-operator", "password": strings.Repeat("界", 40)}
	setupRequest(t, h, "POST", "/api/auth/login", login, 503)
	form := map[string]string{"code": "incorrect", "username": login["username"], "password": login["password"]}
	setupRequest(t, h, "POST", "/api/setup", form, 403)
	form["code"] = code
	setupRequest(t, h, "POST", "/api/setup", form, 204)
	setupRequest(t, h, "POST", "/api/setup", form, 409)
	if _, err := os.Stat(filepath.Join(filepath.Dir(statePath), setupCodeFile)); !os.IsNotExist(err) {
		t.Fatal("setup code file was not removed")
	}
	if got := setupRequest(t, h, "GET", "/setup", nil, 303).Header().Get("Location"); got != "/login" {
		t.Fatalf("completed setup redirected to %q", got)
	}
	setupRequest(t, h, "POST", "/api/auth/login", login, 204)
	data, err := os.ReadFile(statePath)
	if err != nil || bytes.Contains(data, []byte(login["password"])) {
		t.Fatal("expected saved state without plaintext credentials")
	}

	restarted, err := NewStartupHandler(Credentials{Hub: &HubConfig{Store: setupStore(t, statePath)}}, assets, statePath, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if got := setupRequest(t, restarted, "GET", "/api/setup", nil, 200).Body.String(); !strings.Contains(got, `"required":false`) {
		t.Fatal("setup reopened after restart")
	}
	setupRequest(t, restarted, "POST", "/api/auth/login", login, 204)
}

func TestStartupMigratesAndExplicitlyResetsAdministrator(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "hub", "state.json")
	store := setupStore(t, statePath)
	legacy := Credentials{Username: "legacy", Password: "legacy password text", Hub: &HubConfig{Store: store}}
	old, err := NewHandler(legacy, nil)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	before, ok := store.LocalPlatformOwner()
	if !ok || before.PasswordHash != "" {
		t.Fatal("expected a legacy administrator")
	}
	if _, err := NewStartupHandler(Credentials{Hub: legacy.Hub}, nil, statePath, false); err == nil {
		t.Fatal("existing legacy administrator reopened setup")
	}
	migrated, err := NewStartupHandler(legacy, nil, statePath, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(migrated.Close)
	previous := map[string]string{"username": legacy.Username, "password": legacy.Password}
	setupRequest(t, migrated, "POST", "/api/auth/login", previous, 204)

	updated := Credentials{Username: "new-operator", Password: "new password text", Hub: &HubConfig{Store: setupStore(t, statePath)}}
	newLogin := map[string]string{"username": updated.Username, "password": updated.Password}
	unchanged, err := NewStartupHandler(updated, nil, statePath, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unchanged.Close)
	setupRequest(t, unchanged, "POST", "/api/auth/login", previous, 204)
	setupRequest(t, unchanged, "POST", "/api/auth/login", newLogin, 401)

	reset, err := NewStartupHandler(updated, nil, statePath, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reset.Close)
	setupRequest(t, reset, "POST", "/api/auth/login", previous, 401)
	setupRequest(t, reset, "POST", "/api/auth/login", newLogin, 204)
	after, ok := updated.Hub.Store.LocalPlatformOwner()
	if !ok || before.ID != after.ID || before.UserID != after.UserID || after.PasswordHash == "" {
		t.Fatal("credential reset changed the administrator identity")
	}
	legacy.Hub = &HubConfig{Store: setupStore(t, statePath)}
	direct, err := NewHandler(legacy, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(direct.Close)
	setupRequest(t, direct, "POST", "/api/auth/login", previous, 401)
	setupRequest(t, direct, "POST", "/api/auth/login", newLogin, 204)
}
