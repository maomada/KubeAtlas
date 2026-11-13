package portal

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

type mockOIDCIssuer struct {
	mu        sync.Mutex
	server    *httptest.Server
	key       *rsa.PrivateKey
	challenge string
	nonce     string
	badNonce  bool
}

func newMockOIDCIssuer(t *testing.T) *mockOIDCIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockOIDCIssuer{key: key}
	mux := http.NewServeMux()
	mock.server = httptest.NewServer(mux)
	issuer := mock.server.URL
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
			"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/jwks",
			"end_session_endpoint": issuer + "/logout",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		exponent := big.NewInt(int64(key.PublicKey.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test-key",
			"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(exponent),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.ParseForm() != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		mock.mu.Lock()
		challenge, nonce, badNonce := mock.challenge, mock.nonce, mock.badNonce
		mock.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		clientID, clientSecret, hasBasicAuth := r.BasicAuth()
		if !hasBasicAuth {
			clientID, clientSecret = r.Form.Get("client_id"), r.Form.Get("client_secret")
		}
		if r.Form.Get("code") != "authorization-code" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != challenge ||
			clientID != "test-client" || clientSecret != "not-for-logs" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if badNonce {
			nonce = "incorrect-nonce"
		}
		idToken := mock.signIDToken(t, issuer, "subject-never-exposed", "test-client", nonce)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-token", "token_type": "Bearer", "expires_in": 300, "id_token": idToken,
		})
	})
	t.Cleanup(mock.server.Close)
	return mock
}

func (m *mockOIDCIssuer) signIDToken(t *testing.T, issuer, subject, clientID, nonce string) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "test-key"})
	claims, _ := json.Marshal(map[string]any{
		"iss": issuer, "sub": subject, "aud": clientID,
		"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
		"nonce": nonce, "preferred_username": "Alice",
	})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	hash := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, m.key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestOIDCLoginPKCENonceAndOneUseState(t *testing.T) {
	issuer := newMockOIDCIssuer(t)
	store, err := hubstate.OpenStore(filepath.Join(t.TempDir(), "hub", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	hub, err := newPortalHub(Credentials{Username: "local-owner", Hub: &HubConfig{Store: store}})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.transport.Close()
	config := OIDCConfig{
		Issuer: issuer.server.URL, ClientID: "test-client", ClientSecret: "not-for-logs",
		RedirectURL: issuer.server.URL + "/api/auth/oidc/callback",
	}
	authenticator, err := newOIDCAuthenticator(config, false)
	if err != nil {
		t.Fatal(err)
	}
	defer authenticator.transport.CloseIdleConnections()
	handler := &Handler{liveHub: hub, oidc: authenticator, sessions: newViewerSessions()}
	login := httptest.NewRecorder()
	loginURL := strings.TrimSuffix(config.RedirectURL, "/api/auth/oidc/callback") + "/api/auth/oidc/login"
	handler.oidcLogin(login, httptest.NewRequest(http.MethodGet, loginURL, nil))
	if login.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d", login.Code)
	}
	authorization, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := authorization.Query()
	challenge := query.Get("code_challenge")
	if query.Get("code_challenge_method") != "S256" || challenge == "" || query.Get("prompt") != "login" {
		t.Fatal("authorization request did not require S256 PKCE and a fresh login")
	}
	state, nonce := query.Get("state"), query.Get("nonce")
	issuer.mu.Lock()
	issuer.challenge, issuer.nonce = challenge, nonce
	issuer.mu.Unlock()
	var stateCookie *http.Cookie
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == handler.oidcStateCookieName() {
			stateCookie = cookie
		}
	}
	if stateCookie == nil {
		t.Fatal("browser-bound state cookie was not set")
	}
	callbackURL := config.RedirectURL + "?code=authorization-code&state=" + url.QueryEscape(state)
	wrongAuthority := httptest.NewRequest(http.MethodGet,
		"http://localhost/api/auth/oidc/callback?code=authorization-code&state="+url.QueryEscape(state), nil)
	wrongAuthority.AddCookie(stateCookie)
	wrongAuthority.Header.Set("Sec-Fetch-Site", "cross-site")
	rejected := httptest.NewRecorder()
	handler.oidcCallback(rejected, wrongAuthority)
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("wrong callback authority status=%d", rejected.Code)
	}
	callback := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	callback.Header.Set("Sec-Fetch-Site", "cross-site")
	callback.AddCookie(stateCookie)
	result := httptest.NewRecorder()
	handler.oidcCallback(result, callback)
	if result.Code != http.StatusSeeOther || result.Header().Get("Location") != "/" {
		t.Fatalf("callback status/location = %d %q", result.Code, result.Header().Get("Location"))
	}
	memberID := oidcMemberID(config.Issuer, "subject-never-exposed")
	account, ok := store.Account(memberID)
	if !ok || account.Status != "pending" || account.DisplayName != "Alice" ||
		strings.Contains(account.ID, "subject-never-exposed") || strings.Contains(account.UserID, "subject-never-exposed") ||
		len(store.OrganizationsForAccount(memberID)) != 0 {
		t.Fatalf("OIDC account = %#v, ok=%v", account, ok)
	}
	replay := httptest.NewRecorder()
	handler.oidcCallback(replay, callback)
	if replay.Code != http.StatusSeeOther || replay.Header().Get("Location") != "/login?error=sso_failed" {
		t.Fatal("consumed OIDC state was accepted again")
	}
}

func TestOIDCRejectsPKCEAndNonceMismatch(t *testing.T) {
	for _, mismatch := range []string{"pkce", "nonce"} {
		t.Run(mismatch, func(t *testing.T) {
			issuer := newMockOIDCIssuer(t)
			store, err := hubstate.OpenStore(filepath.Join(t.TempDir(), "hub", "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			hub, err := newPortalHub(Credentials{Username: "local-owner", Hub: &HubConfig{Store: store}})
			if err != nil {
				t.Fatal(err)
			}
			defer hub.transport.Close()
			config := OIDCConfig{
				Issuer: issuer.server.URL, ClientID: "test-client", ClientSecret: "not-for-logs",
				RedirectURL: issuer.server.URL + "/api/auth/oidc/callback",
			}
			authenticator, err := newOIDCAuthenticator(config, false)
			if err != nil {
				t.Fatal(err)
			}
			defer authenticator.transport.CloseIdleConnections()
			handler := &Handler{liveHub: hub, oidc: authenticator, sessions: newViewerSessions()}
			login := httptest.NewRecorder()
			loginURL := strings.TrimSuffix(config.RedirectURL, "/api/auth/oidc/callback") + "/api/auth/oidc/login"
			handler.oidcLogin(login, httptest.NewRequest(http.MethodGet, loginURL, nil))
			authorization, _ := url.Parse(login.Header().Get("Location"))
			query := authorization.Query()
			issuer.mu.Lock()
			issuer.challenge, issuer.nonce = query.Get("code_challenge"), query.Get("nonce")
			if mismatch == "nonce" {
				issuer.badNonce = true
			}
			issuer.mu.Unlock()
			if mismatch == "pkce" {
				key := sha256.Sum256([]byte(query.Get("state")))
				authenticator.mu.Lock()
				transaction := authenticator.transactions[key]
				transaction.verifier = "incorrect-verifier-value"
				authenticator.transactions[key] = transaction
				authenticator.mu.Unlock()
			}
			var stateCookie *http.Cookie
			for _, cookie := range login.Result().Cookies() {
				if cookie.Name == handler.oidcStateCookieName() {
					stateCookie = cookie
				}
			}
			callback := httptest.NewRequest(http.MethodGet,
				config.RedirectURL+"?code=authorization-code&state="+url.QueryEscape(query.Get("state")), nil)
			callback.AddCookie(stateCookie)
			result := httptest.NewRecorder()
			handler.oidcCallback(result, callback)
			memberID := oidcMemberID(config.Issuer, "subject-never-exposed")
			_, accountCreated := store.Account(memberID)
			if result.Code != http.StatusSeeOther || result.Header().Get("Location") != "/login?error=sso_failed" || accountCreated {
				t.Fatal("mismatched OIDC transaction was accepted")
			}
		})
	}
}
