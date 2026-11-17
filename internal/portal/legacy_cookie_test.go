package portal

import (
	"github.com/maomada/KubeAtlas/pkg/compat"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeployedPortalCookieRotationDoesNotMergeSessions(t *testing.T) {
	for _, secure := range []bool{true, false} {
		t.Run(map[bool]string{true: "secure", false: "loopback"}[secure], func(t *testing.T) {
			h, _ := newPortalHubFixture(t, false)
			h.secureCookies = secure
			current := loginForTest(t, h)
			old := *current
			old.Name = compat.LegacyPortalCookie
			if strings.HasPrefix(current.Name, "__Host-") {
				old.Name = "__Host-" + old.Name
			}
			r := httptest.NewRequest("GET", "http://localhost/api/auth/session", nil)
			r.AddCookie(&old)
			if !h.hasViewerSession(r) {
				t.Fatal("existing session token not recognized under deployed cookie name")
			}
			r.AddCookie(current)
			if h.hasViewerSession(r) {
				t.Fatal("mixed cookies accepted")
			}
			logout := httptest.NewRequest("POST", "http://localhost/api/auth/logout", nil)
			logout.Header.Set("Origin", "http://localhost")
			logout.Header.Set("Sec-Fetch-Site", "same-origin")
			logout.Header.Set("Content-Type", "application/json")
			logout.AddCookie(&old)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, logout)
			if w.Code != http.StatusNoContent {
				t.Fatalf("logout status=%d", w.Code)
			}
			cleared := map[string]bool{}
			for _, c := range w.Result().Cookies() {
				if c.MaxAge < 0 {
					cleared[c.Name] = true
				}
			}
			if !cleared[h.cookieNameForRequest(logout)] || !cleared[old.Name] {
				t.Fatal("logout did not clear both names")
			}
			check := httptest.NewRequest("GET", "http://localhost/api/auth/session", nil)
			check.AddCookie(&old)
			if h.hasViewerSession(check) {
				t.Fatal("old cookie resurrected revoked session")
			}
		})
	}
}
