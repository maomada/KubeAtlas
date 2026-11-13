package portal

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func TestNormalizeHTTPIPOrigin(t *testing.T) {
	for input, want := range map[string]string{
		"http://192.0.2.10:9290":               "http://192.0.2.10:9290",
		"http://192.0.2.10:80":                 "http://192.0.2.10:80",
		"http://[2001:db8::10]:9290":           "http://[2001:db8::10]:9290",
		"http://[2001:0DB8:0:0:0:0:0:10]:9290": "http://[2001:db8::10]:9290",
	} {
		got, err := normalizeHTTPIPOrigin(input)
		if err != nil || got != want {
			t.Errorf("normalizeHTTPIPOrigin(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{
		"http://localhost:9290", "http://portal.example.test", "https://192.0.2.10",
		"http://user@192.0.2.10", "http://192.0.2.10/", "http://192.0.2.10/path",
		"http://192.0.2.10?", "http://192.0.2.10#fragment", "http://192.0.2.10#", "http://192.0.2.10:0",
		"http://192.0.2.10:65536", "http://192.0.2.10:09290", "http://192.0.2.10:", "http://[192.0.2.10]",
		"http://0192.0.2.10", "http://127.1",
		"http://192.0.2.10:abc",
		"http://2001:db8::10", "http://[fe80::1%25eth0]", "http://192.0.2.10\\@example.test",
		" http://192.0.2.10", "http://192.0.2.10\n",
	} {
		if got, err := normalizeHTTPIPOrigin(input); err == nil {
			t.Errorf("normalizeHTTPIPOrigin(%q) = %q, want rejection", input, got)
		}
	}
}

func TestHandlerRejectsInvalidHTTPIPOriginAtStartup(t *testing.T) {
	store, err := hubstate.OpenStore(filepath.Join(t.TempDir(), "hub", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewHandler(Credentials{
		Username: testUsername, Password: testPassword, HTTPIPOrigin: "http://portal.example.test",
		Hub: &HubConfig{Store: store},
	}, nil)
	if err == nil {
		t.Fatal("Handler accepted a DNS HTTP origin")
	}
}

func httpIPTestHandler(t *testing.T, origin string) *Handler {
	t.Helper()
	store, err := hubstate.OpenStore(filepath.Join(t.TempDir(), "hub", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(Credentials{
		Username: testUsername, Password: testPassword, HTTPIPOrigin: origin,
		Hub: &HubConfig{Store: store},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func httpIPLogin(h *Handler, origin string, mutate func(*http.Request)) (*httptest.ResponseRecorder, *http.Request) {
	body, _ := json.Marshal(map[string]string{"username": testUsername, "password": testPassword})
	r := httptest.NewRequest(http.MethodPost, origin+"/api/auth/login", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w, r
}

func TestHTTPIPOriginAllowsFreshStoreLoginWithScopedCookies(t *testing.T) {
	for _, origin := range []string{"http://192.0.2.10:9290", "http://[2001:db8::10]:9290"} {
		t.Run(origin, func(t *testing.T) {
			h := httpIPTestHandler(t, origin)
			w, r := httpIPLogin(h, origin, func(r *http.Request) {
				r.Header.Set("X-Forwarded-Host", "attacker.example")
				r.Header.Set("X-Forwarded-Proto", "https")
			})
			if w.Code != http.StatusNoContent {
				t.Fatalf("fresh-store login status=%d body=%s", w.Code, w.Body.String())
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("login returned %d cookies, want 1", len(cookies))
			}
			cookie := cookies[0]
			if cookie.Name != "radar_portal_session" || cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" || cookie.Path != "/" {
				t.Fatalf("unsafe HTTP-IP session cookie attributes: %#v", cookie)
			}
			if !h.liveHub.policySnapshot().allows(r) || h.liveHub.store.Connections().LANURL != "" || h.liveHub.store.Connections().PublicURL != "" {
				t.Fatal("startup HTTP origin was not allowed independently of persisted connections")
			}
			if h.httpIPOrigin != origin {
				t.Fatalf("stored origin=%q, want normalized %q", h.httpIPOrigin, origin)
			}
		})
	}
}

func TestHTTPIPOriginAuthorityAndHTTPSCookieBoundaries(t *testing.T) {
	origin := "http://192.0.2.10:9290"
	h := httpIPTestHandler(t, origin)
	for _, host := range []string{"192.0.2.11:9290", "192.0.2.10:9291"} {
		r := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/auth/login", strings.NewReader(`{}`))
		if !h.cookieSecureForRequest(r) || h.liveHub.policySnapshot().allows(r) {
			t.Errorf("unconfigured authority %q was trusted", host)
		}
	}
	dnsRequest := httptest.NewRequest(http.MethodGet, "http://portal.example.test/", nil)
	if !h.cookieSecureForRequest(dnsRequest) || h.liveHub.policySnapshot().allows(dnsRequest) {
		t.Fatal("unconfigured HTTPS DNS authority was not kept Secure and denied")
	}
	wrong, _ := httpIPLogin(h, "http://192.0.2.11:9290", nil)
	if wrong.Code != http.StatusForbidden {
		t.Fatalf("wrong authority login status=%d, want 403", wrong.Code)
	}
	wrongPort, _ := httpIPLogin(h, "http://192.0.2.10:9291", nil)
	if wrongPort.Code != http.StatusForbidden {
		t.Fatalf("wrong port login status=%d, want 403", wrongPort.Code)
	}
	noOptIn := httpIPTestHandler(t, "")
	w, _ := httpIPLogin(noOptIn, origin, nil)
	if w.Code != http.StatusForbidden || !noOptIn.cookieSecureForRequest(httptest.NewRequest(http.MethodGet, origin, nil)) {
		t.Fatal("non-loopback HTTP without opt-in was not denied and Secure")
	}
	r := httptest.NewRequest(http.MethodGet, origin, nil)
	r.TLS = &tls.ConnectionState{}
	if !h.cookieSecureForRequest(r) {
		t.Fatal("direct TLS request did not require Secure cookies")
	}
	body, _ := json.Marshal(map[string]string{"username": testUsername, "password": testPassword})
	tlsRequest := httptest.NewRequest(http.MethodPost, "https://192.0.2.10:9290/api/auth/login", strings.NewReader(string(body)))
	tlsRequest.Header.Set("Content-Type", "application/json")
	tlsRequest.Header.Set("Origin", "https://192.0.2.10:9290")
	tlsRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	tlsResponse := httptest.NewRecorder()
	h.ServeHTTP(tlsResponse, tlsRequest)
	if tlsResponse.Code != http.StatusNoContent || len(tlsResponse.Result().Cookies()) != 1 || !tlsResponse.Result().Cookies()[0].Secure {
		t.Fatalf("direct TLS login cookie was not Secure: status=%d cookies=%#v", tlsResponse.Code, tlsResponse.Result().Cookies())
	}
	if _, err := h.liveHub.store.UpdateConnections(hubstate.ConnectionSettings{PublicURL: "https://192.0.2.10:9290"}); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodGet, origin, nil)
	if !h.cookieSecureForRequest(r) {
		t.Fatal("HTTPS profile sharing the HTTP authority did not take precedence")
	}
	mixedResponse, _ := httpIPLogin(h, origin, nil)
	if mixedResponse.Code != http.StatusNoContent || len(mixedResponse.Result().Cookies()) != 1 || !mixedResponse.Result().Cookies()[0].Secure {
		t.Fatalf("mixed HTTPS profile did not produce a Secure cookie: status=%d cookies=%#v", mixedResponse.Code, mixedResponse.Result().Cookies())
	}
}

func TestInsecureCookieOptInRemainsLoopbackOnly(t *testing.T) {
	store, err := hubstate.OpenStore(filepath.Join(t.TempDir(), "hub", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(Credentials{Username: testUsername, Password: testPassword, AllowInsecureCookies: true, Hub: &HubConfig{Store: store}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	if h.cookieSecureForRequest(httptest.NewRequest(http.MethodGet, "http://localhost/", nil)) {
		t.Fatal("loopback insecure-cookie option did not retain its behavior")
	}
	if !h.cookieSecureForRequest(httptest.NewRequest(http.MethodGet, "http://192.0.2.10/", nil)) {
		t.Fatal("insecure-cookie option allowed a non-loopback authority")
	}
}
