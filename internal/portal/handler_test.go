package portal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

const (
	testUsername = "portal-viewer-user"
	testPassword = "portal-viewer-password"
)

func testHandler(t *testing.T, statePath string) (*Handler, string) {
	t.Helper()
	store, err := hubstate.OpenStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Credentials{
		Username: testUsername, Password: testPassword, Hub: &HubConfig{Store: store},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		handler.Close()
	})
	return handler, statePath
}

func TestHandlerRequiresHubConfiguration(t *testing.T) {
	if _, err := NewHandler(Credentials{Username: testUsername, Password: testPassword}, nil); err == nil {
		t.Fatal("handler accepted missing Hub configuration")
	}
}

func TestHandlerCreatesOnlyHubState(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "hub", "state.json")
	_, _ = testHandler(t, statePath)
	for _, legacyPath := range []string{
		filepath.Join(root, "registry.json"),
		filepath.Join(root, "fleet"),
		filepath.Join(root, "snapshots"),
	} {
		if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
			t.Errorf("handler created or opened retired storage path %s: %v", legacyPath, err)
		}
	}
}

func TestRemovedPortalRoutesReturnNotFoundForLocalOwner(t *testing.T) {
	handler, _ := testHandler(t, filepath.Join(t.TempDir(), "hub", "state.json"))
	cookie := loginForTest(t, handler)
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/fleet/clusters"},
		{http.MethodPost, "/api/fleet/snapshots/alpha"},
		{http.MethodPut, "/api/registrations/alpha"},
		{http.MethodGet, "/api/clusters"},
		{http.MethodPost, "/mcp-fleet"},
		{http.MethodGet, "/fleet"},
	} {
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404", tc.method, tc.path, response.Code)
		}
	}
}

func TestOwnerAuthHealthAndEmbeddedAssetServing(t *testing.T) {
	handler, _ := testHandler(t, filepath.Join(t.TempDir(), "hub", "state.json"))
	handler.static = fstest.MapFS{
		"portal.html":   &fstest.MapFile{Data: []byte("<html>portal</html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('portal')")},
	}
	for _, path := range []string{"/login", "/assets/app.js"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200: %s", path, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login" {
		t.Fatal("unauthenticated root did not redirect to login")
	}
	request = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("/healthz status = %d, want 204", response.Code)
	}
}
