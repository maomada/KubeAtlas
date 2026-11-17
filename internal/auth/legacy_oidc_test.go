package auth

import (
	"github.com/maomada/KubeAtlas/pkg/compat"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOIDCCompatibilityKeepsPKCEInOneCookieFamily(t *testing.T) {
	for _, tc := range []struct {
		name, state, verifier string
		duplicate, valid      bool
	}{
		{"old", compat.LegacyOIDCStateCookie, compat.LegacyOIDCVerifierCookie, false, true},
		{"new", oidcStateCookieName, oidcVerifierCookieName, false, true},
		{"mixed", oidcStateCookieName, compat.LegacyOIDCVerifierCookie, false, false},
		{"duplicate", compat.LegacyOIDCStateCookie, compat.LegacyOIDCVerifierCookie, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var called atomic.Bool
			issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called.Store(true)
				_ = r.ParseForm()
				if r.Form.Get("code_verifier") != "fixture-verifier" {
					t.Error("PKCE verifier changed")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"fixture_stopped"}`))
			}))
			defer issuer.Close()
			h := newTestOIDCHandler()
			h.pkceEnabled = true
			h.oauth = oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: issuer.URL}}
			r := httptest.NewRequest("GET", "http://localhost/auth/callback?state=fixture-state&code=fixture-code", nil)
			r.AddCookie(&http.Cookie{Name: tc.state, Value: "fixture-state"})
			r.AddCookie(&http.Cookie{Name: tc.verifier, Value: "fixture-verifier"})
			if tc.duplicate {
				r.AddCookie(&http.Cookie{Name: oidcStateCookieName, Value: "fixture-state"})
			}
			w := httptest.NewRecorder()
			h.HandleCallback(w, r)
			if called.Load() != tc.valid {
				t.Fatalf("token exchange reached=%v want=%v status=%d", called.Load(), tc.valid, w.Code)
			}
			if !tc.valid && w.Code != 400 {
				t.Fatalf("invalid cookie family status=%d", w.Code)
			}
		})
	}
}
