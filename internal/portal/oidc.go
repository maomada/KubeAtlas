package portal

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/maomada/KubeAtlas/pkg/compat"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	oidcTransactionLifetime = 5 * time.Minute
	maxOIDCTransactions     = 1024
	maxOIDCResponseBytes    = 1 << 20
)

type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	CACertFile   string
}

type oidcDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

type oidcTransaction struct {
	verifier string
	nonce    string
	expires  time.Time
}

type oidcAuthenticator struct {
	config       OIDCConfig
	oauth        oauth2.Config
	verifier     *oidc.IDTokenVerifier
	client       *http.Client
	transport    *http.Transport
	logoutURL    string
	portalOrigin string
	secureCookie bool
	mu           sync.Mutex
	transactions map[[32]byte]oidcTransaction
	now          func() time.Time
}

func newOIDCAuthenticator(config OIDCConfig, secureCookie bool) (*oidcAuthenticator, error) {
	if err := validateOIDCConfig(config, secureCookie); err != nil {
		return nil, err
	}
	client, transport, err := oidcHTTPClient(config.CACertFile, !secureCookie)
	if err != nil {
		return nil, err
	}
	discoveryURL := strings.TrimRight(config.Issuer, "/") + "/.well-known/openid-configuration"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	response, err := client.Do(request)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, errors.New("OIDC discovery failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		transport.CloseIdleConnections()
		return nil, errors.New("OIDC discovery failed")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOIDCResponseBytes+1))
	if err != nil || len(body) > maxOIDCResponseBytes {
		transport.CloseIdleConnections()
		return nil, errors.New("OIDC discovery response is invalid")
	}
	var discovery oidcDiscovery
	if json.Unmarshal(body, &discovery) != nil || discovery.Issuer != config.Issuer ||
		discovery.AuthorizationEndpoint == "" || discovery.TokenEndpoint == "" ||
		discovery.JWKSURI == "" || discovery.EndSessionEndpoint == "" {
		transport.CloseIdleConnections()
		return nil, errors.New("OIDC discovery metadata is invalid")
	}
	for _, endpoint := range []string{
		discovery.AuthorizationEndpoint, discovery.TokenEndpoint,
		discovery.JWKSURI, discovery.EndSessionEndpoint,
	} {
		if !validOIDCEndpoint(endpoint, !secureCookie) {
			transport.CloseIdleConnections()
			return nil, errors.New("OIDC discovery metadata is invalid")
		}
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(context.Background(), client), config.Issuer)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, errors.New("OIDC provider initialization failed")
	}
	endpoint := provider.Endpoint()
	if !validOIDCEndpoint(endpoint.AuthURL, !secureCookie) ||
		!validOIDCEndpoint(endpoint.TokenURL, !secureCookie) {
		transport.CloseIdleConnections()
		return nil, errors.New("OIDC discovery metadata is invalid")
	}
	redirect, _ := url.Parse(config.RedirectURL)
	return &oidcAuthenticator{
		config: config,
		oauth: oauth2.Config{
			ClientID: config.ClientID, ClientSecret: config.ClientSecret,
			Endpoint: endpoint, RedirectURL: config.RedirectURL,
			Scopes: []string{oidc.ScopeOpenID, "profile"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: config.ClientID}),
		client:   client, transport: transport,
		logoutURL:    discovery.EndSessionEndpoint,
		portalOrigin: redirect.Scheme + "://" + redirect.Host,
		secureCookie: secureCookie, transactions: make(map[[32]byte]oidcTransaction),
		now: time.Now,
	}, nil
}

func validateOIDCConfig(config OIDCConfig, secureCookie bool) error {
	if strings.TrimSpace(config.Issuer) == "" || strings.TrimSpace(config.ClientID) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" || strings.TrimSpace(config.RedirectURL) == "" {
		return errors.New("OIDC issuer, client ID, client secret, and redirect URL are required together")
	}
	issuer, err := url.Parse(config.Issuer)
	if !validOIDCEndpoint(config.Issuer, !secureCookie) || issuer.RawQuery != "" || issuer.ForceQuery {
		return errors.New("OIDC issuer URL must use HTTPS")
	}
	redirect, err := url.Parse(config.RedirectURL)
	if err != nil || redirect.User != nil || redirect.Host == "" || redirect.RawQuery != "" ||
		redirect.ForceQuery || redirect.Fragment != "" || redirect.Path != "/api/auth/oidc/callback" ||
		!validOIDCEndpoint(config.RedirectURL, !secureCookie) {
		return errors.New("OIDC redirect URL must be a fixed HTTPS callback URL")
	}
	if len(config.ClientID) > 1024 || len(config.ClientSecret) > 4096 {
		return errors.New("OIDC client configuration is invalid")
	}
	return nil
}

func validOIDCEndpoint(value string, allowLoopbackHTTP bool) bool {
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return false
	}
	if endpoint.Scheme == "https" {
		return true
	}
	if endpoint.Scheme != "http" || !allowLoopbackHTTP {
		return false
	}
	if strings.EqualFold(endpoint.Hostname(), "localhost") {
		return true
	}
	ip := net.ParseIP(endpoint.Hostname())
	return ip != nil && ip.IsLoopback()
}

func oidcHTTPClient(caFile string, allowLoopbackHTTP bool) (*http.Client, *http.Transport, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil || !roots.AppendCertsFromPEM(pem) {
			return nil, nil, errors.New("OIDC CA certificate is invalid")
		}
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
	}
	client := &http.Client{
		Transport: &boundedOIDCRoundTripper{base: transport, allowLoopbackHTTP: allowLoopbackHTTP},
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("OIDC redirects are not followed")
		},
	}
	return client, transport, nil
}

type boundedOIDCRoundTripper struct {
	base              http.RoundTripper
	allowLoopbackHTTP bool
}

func (t *boundedOIDCRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil || !validOIDCEndpoint(request.URL.String(), t.allowLoopbackHTTP) {
		return nil, errors.New("OIDC endpoint is not permitted")
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOIDCResponseBytes+1))
	_ = response.Body.Close()
	if err != nil || len(body) > maxOIDCResponseBytes {
		return nil, errors.New("OIDC response exceeded its limit")
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

func (h *Handler) oidcStateCookieName() string {
	if h.secureCookies {
		return "__Host-kubeatlas_oidc_state"
	}
	return "kubeatlas_oidc_state"
}

func (h *Handler) oidcStateCookieNameForRequest(r *http.Request) string {
	if h.cookieSecureForRequest(r) {
		return "__Host-kubeatlas_oidc_state"
	}
	return "kubeatlas_oidc_state"
}

func (a *oidcAuthenticator) newTransaction() (string, string, string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", "", err
	}
	state := base64.RawURLEncoding.EncodeToString(random[:])
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", "", err
	}
	verifier := base64.RawURLEncoding.EncodeToString(random[:])
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(random[:])
	key := sha256.Sum256([]byte(state))
	a.mu.Lock()
	now := a.now()
	for existing, transaction := range a.transactions {
		if !now.Before(transaction.expires) {
			delete(a.transactions, existing)
		}
	}
	if len(a.transactions) >= maxOIDCTransactions {
		a.mu.Unlock()
		return "", "", "", errors.New("OIDC transaction capacity reached")
	}
	a.transactions[key] = oidcTransaction{verifier: verifier, nonce: nonce, expires: now.Add(oidcTransactionLifetime)}
	a.mu.Unlock()
	return state, verifier, nonce, nil
}

func (a *oidcAuthenticator) consume(state string) (oidcTransaction, bool) {
	if len(state) != 43 {
		return oidcTransaction{}, false
	}
	if _, err := base64.RawURLEncoding.Strict().DecodeString(state); err != nil {
		return oidcTransaction{}, false
	}
	key := sha256.Sum256([]byte(state))
	a.mu.Lock()
	defer a.mu.Unlock()
	transaction, ok := a.transactions[key]
	delete(a.transactions, key)
	if !ok || !a.now().Before(transaction.expires) {
		return oidcTransaction{}, false
	}
	return transaction, true
}

func (h *Handler) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if h.oidc == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || !h.liveHub.policySnapshot().allows(r) || !h.sessions.allowLogin() {
		http.Error(w, "OIDC login is unavailable", http.StatusForbidden)
		return
	}
	state, verifier, nonce, err := h.oidc.newTransaction()
	if err != nil {
		http.Error(w, "OIDC login is unavailable", http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: legacyOIDCStateCookieName(h, r), Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.cookieSecureForRequest(r), SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{
		Name: h.oidcStateCookieNameForRequest(r), Value: state, Path: "/", MaxAge: int(oidcTransactionLifetime.Seconds()),
		HttpOnly: true, Secure: h.cookieSecureForRequest(r), SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, h.oidc.oauth.AuthCodeURL(
		state, oauth2.AccessTypeOnline, oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.SetAuthURLParam("prompt", "login"),
		oauth2.S256ChallengeOption(verifier),
	), http.StatusSeeOther)
}

func callbackQuery(rawQuery string) (url.Values, bool) {
	if len(rawQuery) > 8192 {
		return nil, false
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, false
	}
	for key, entries := range values {
		if len(entries) != 1 || !map[string]bool{
			"code": true, "state": true, "iss": true, "session_state": true,
			"error": true, "error_description": true, "error_uri": true,
		}[key] {
			return nil, false
		}
	}
	return values, true
}

func (h *Handler) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if h.oidc == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.oidcCallbackAuthorityAllowed(r) {
		http.Error(w, "OIDC callback authority is not allowed", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: legacyOIDCStateCookieName(h, r), Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.cookieSecureForRequest(r), SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{
		Name: h.oidcStateCookieNameForRequest(r), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: h.cookieSecureForRequest(r), SameSite: http.SameSiteLaxMode,
	})
	fail := func() {
		http.Redirect(w, r, "/login?error=sso_failed", http.StatusSeeOther)
	}
	values, ok := callbackQuery(r.URL.RawQuery)
	if !ok || values.Get("error") != "" || values.Get("code") == "" ||
		len(values.Get("code")) > 4096 || values.Get("state") == "" {
		fail()
		return
	}
	if issuer := values.Get("iss"); issuer != "" && issuer != h.oidc.config.Issuer {
		fail()
		return
	}
	state := values.Get("state")
	cookies := 0
	cookieState := ""
	for _, cookie := range r.Cookies() {
		legacyName := compat.LegacyOIDCStateCookie
		if h.cookieSecureForRequest(r) {
			legacyName = "__Host-" + legacyName
		}
		if cookie.Name == h.oidcStateCookieNameForRequest(r) || cookie.Name == legacyName {
			cookies++
			cookieState = cookie.Value
		}
	}
	if cookies != 1 || subtle.ConstantTimeCompare([]byte(state), []byte(cookieState)) != 1 {
		fail()
		return
	}
	transaction, ok := h.oidc.consume(state)
	if !ok {
		fail()
		return
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(r.Context(), h.oidc.client), 10*time.Second)
	defer cancel()
	token, err := h.oidc.oauth.Exchange(ctx, values.Get("code"), oauth2.VerifierOption(transaction.verifier))
	if err != nil {
		fail()
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" || len(rawIDToken) > 64<<10 {
		fail()
		return
	}
	idToken, err := h.oidc.verifier.Verify(ctx, rawIDToken)
	if err != nil || subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(transaction.nonce)) != 1 ||
		idToken.Subject == "" || len(idToken.Subject) > 4096 || !utf8.ValidString(idToken.Subject) {
		fail()
		return
	}
	var claims struct {
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
	}
	if idToken.Claims(&claims) != nil {
		fail()
		return
	}
	memberID := oidcMemberID(h.oidc.config.Issuer, idToken.Subject)
	displayName := "SSO member"
	if validOIDCDisplayName(claims.PreferredUsername) {
		displayName = claims.PreferredUsername
	} else if validOIDCDisplayName(claims.Name) {
		displayName = claims.Name
	}
	member, _, err := h.liveHub.store.UpsertOIDCMember(memberID, displayName)
	if err != nil || member.Status == "disabled" {
		fail()
		return
	}
	expires := h.sessions.now().Add(sessionLifetime)
	if idToken.Expiry.Before(expires) {
		expires = idToken.Expiry
	}
	if !h.createSession(w, r, member.ID, expires) {
		fail()
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) oidcCallbackAuthorityAllowed(r *http.Request) bool {
	if h.oidc == nil || r.URL.Path != "/api/auth/oidc/callback" {
		return false
	}
	expected, err := url.Parse(h.oidc.config.RedirectURL)
	if err != nil || expected.Host == "" || expected.Path != r.URL.Path {
		return false
	}
	if _, ok := hubAuthority(r.Host); !ok {
		return false
	}
	return strings.EqualFold(expected.Host, r.Host)
}

func oidcMemberID(issuer, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + subject))
	return "oidc:" + hex.EncodeToString(sum[:])
}

func validOIDCDisplayName(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func (h *Handler) oidcLogoutURL() string {
	endpoint, err := url.Parse(h.oidc.logoutURL)
	if err != nil {
		return "/login?error=sso_failed"
	}
	query := endpoint.Query()
	query.Set("client_id", h.oidc.config.ClientID)
	query.Set("post_logout_redirect_uri", h.oidc.portalOrigin+"/login")
	endpoint.RawQuery = query.Encode()
	return endpoint.String()
}
