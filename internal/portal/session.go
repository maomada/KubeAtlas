package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"github.com/maomada/KubeAtlas/pkg/compat"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/maomada/KubeAtlas/internal/hubstate"
	"golang.org/x/crypto/bcrypt"
)

const sessionLifetime = 8 * time.Hour
const maxViewerSessions = 1024

type viewerSessions struct {
	mu            sync.Mutex
	expires       map[[32]byte]time.Time
	active        map[[32]byte]*viewerSession
	now           func() time.Time
	loginAttempts []time.Time
}

type viewerSession struct {
	ownerID        string
	organizationID string
	ctx            context.Context
	cancel         context.CancelFunc
	timer          *time.Timer
}

type viewerRequestSnapshotKey struct{}

type viewerRequestSnapshot struct {
	key            [32]byte
	ownerID        string
	organizationID string
	expiresAt      time.Time
	baseCtx        context.Context
	requestContext context.Context
}

func newViewerSessions() *viewerSessions {
	return &viewerSessions{
		expires: make(map[[32]byte]time.Time), active: make(map[[32]byte]*viewerSession), now: time.Now,
	}
}

func (s *viewerSessions) allowLogin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for len(s.loginAttempts) > 0 && !s.loginAttempts[0].After(now.Add(-time.Minute)) {
		s.loginAttempts = s.loginAttempts[1:]
	}
	if len(s.loginAttempts) >= 20 {
		return false
	}
	s.loginAttempts = append(s.loginAttempts, now)
	return true
}

func (h *Handler) cookieName() string {
	if h.secureCookies {
		return "__Host-kubeatlas_portal_session"
	}
	return "kubeatlas_portal_session"
}

func (h *Handler) cookieSecureForRequest(r *http.Request) bool {
	if r.TLS != nil || h.liveHub == nil {
		return true
	}
	authority, err := url.Parse("http://" + r.Host)
	if err != nil || authority.Hostname() == "" {
		return true
	}
	requestIP := net.ParseIP(authority.Hostname())
	httpsAuthority := false
	connections := h.liveHub.store.Connections()
	for _, configured := range []string{connections.LANURL, connections.PublicURL} {
		origin, err := url.Parse(configured)
		if err != nil || origin.Hostname() == "" || !sameCookieAuthority(authority, origin) {
			continue
		}
		if origin.Scheme == "https" {
			httpsAuthority = true
		}
	}
	if httpsAuthority {
		return true
	}
	if h.httpIPOrigin != "" {
		origin, err := url.Parse(h.httpIPOrigin)
		if err == nil && sameCookieAuthority(authority, origin) {
			return false
		}
	}
	if h.secureCookies {
		return true
	}
	if !h.secureCookies && (strings.EqualFold(authority.Hostname(), "localhost") || requestIP != nil && requestIP.IsLoopback()) {
		return false
	}
	return true
}

func sameCookieAuthority(request, configured *url.URL) bool {
	if !strings.EqualFold(strings.TrimSuffix(request.Hostname(), "."), strings.TrimSuffix(configured.Hostname(), ".")) {
		return false
	}
	requestPort, configuredPort := request.Port(), configured.Port()
	if requestPort == "" {
		requestPort = "80"
		if configured.Scheme == "https" {
			requestPort = "443"
		}
	}
	if configuredPort == "" {
		configuredPort = "80"
		if configured.Scheme == "https" {
			configuredPort = "443"
		}
	}
	return requestPort == configuredPort
}

func (h *Handler) cookieNameForRequest(r *http.Request) string {
	if h.cookieSecureForRequest(r) {
		return "__Host-kubeatlas_portal_session"
	}
	return "kubeatlas_portal_session"
}

func (h *Handler) sessionHash(r *http.Request) ([32]byte, bool) {
	legacyName := compat.LegacyPortalCookie
	if h.cookieSecureForRequest(r) {
		legacyName = "__Host-" + legacyName
	}
	cookie, err := compat.SingleCookie(r, h.cookieNameForRequest(r), legacyName)
	if err != nil || len(cookie.Value) != 43 {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(cookie.Value)), true
}

func viewerSnapshot(r *http.Request) (*viewerRequestSnapshot, bool) {
	snapshot, ok := r.Context().Value(viewerRequestSnapshotKey{}).(*viewerRequestSnapshot)
	return snapshot, ok && snapshot != nil
}

func (h *Handler) bindViewerSession(r *http.Request) (*http.Request, func()) {
	key, ok := h.sessionHash(r)
	if !ok {
		return r, func() {}
	}
	h.sessions.mu.Lock()
	expires, exists := h.sessions.expires[key]
	session := h.sessions.active[key]
	if !exists || session == nil || !h.sessions.now().Before(expires) {
		h.sessions.removeLocked(key)
		h.sessions.mu.Unlock()
		return r, func() {}
	}
	snapshot := &viewerRequestSnapshot{key: key, ownerID: session.ownerID, organizationID: session.organizationID,
		expiresAt: expires, baseCtx: session.ctx, requestContext: r.Context()}
	h.sessions.mu.Unlock()
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(snapshot.baseCtx, cancel)
	if snapshot.baseCtx.Err() != nil {
		stop()
		cancel()
		return r, func() {}
	}
	return r.WithContext(context.WithValue(ctx, viewerRequestSnapshotKey{}, snapshot)), func() {
		stop()
		cancel()
	}
}

func (h *Handler) hasViewerSession(r *http.Request) bool {
	if snapshot, ok := viewerSnapshot(r); ok {
		if r.Context().Err() != nil || !h.sessions.now().Before(snapshot.expiresAt) {
			return false
		}
		if h.liveHub == nil {
			return true
		}
		account, exists := h.liveHub.store.Account(snapshot.ownerID)
		if exists && account.Status != "disabled" {
			return true
		}
		h.sessions.mu.Lock()
		h.sessions.removeLocked(snapshot.key)
		h.sessions.mu.Unlock()
		return false
	}
	key, ok := h.sessionHash(r)
	if !ok {
		return false
	}
	h.sessions.mu.Lock()
	expires, exists := h.sessions.expires[key]
	if !exists || !h.sessions.now().Before(expires) {
		h.sessions.removeLocked(key)
		h.sessions.mu.Unlock()
		return false
	}
	session := h.sessions.active[key]
	ownerID := ""
	if session != nil {
		ownerID = session.ownerID
	}
	h.sessions.mu.Unlock()
	if h.liveHub != nil && ownerID != "" {
		account, exists := h.liveHub.store.Account(ownerID)
		if !exists || account.Status == "disabled" {
			h.sessions.mu.Lock()
			h.sessions.removeLocked(key)
			h.sessions.mu.Unlock()
			return false
		}
	}
	return true
}

func (h *Handler) sessionOwnerID(r *http.Request) (string, bool) {
	if snapshot, ok := viewerSnapshot(r); ok {
		return snapshot.ownerID, r.Context().Err() == nil && h.sessions.now().Before(snapshot.expiresAt)
	}
	key, ok := h.sessionHash(r)
	if !ok {
		return "", false
	}
	h.sessions.mu.Lock()
	defer h.sessions.mu.Unlock()
	expires, exists := h.sessions.expires[key]
	session := h.sessions.active[key]
	if !exists || session == nil || !h.sessions.now().Before(expires) {
		h.sessions.removeLocked(key)
		return "", false
	}
	return session.ownerID, true
}

func (h *Handler) sessionOrganizationID(r *http.Request) (string, bool) {
	if snapshot, ok := viewerSnapshot(r); ok {
		return snapshot.organizationID, r.Context().Err() == nil && h.sessions.now().Before(snapshot.expiresAt)
	}
	key, ok := h.sessionHash(r)
	if !ok {
		return "", false
	}
	h.sessions.mu.Lock()
	defer h.sessions.mu.Unlock()
	expires, exists := h.sessions.expires[key]
	session := h.sessions.active[key]
	if !exists || session == nil || !h.sessions.now().Before(expires) {
		h.sessions.removeLocked(key)
		return "", false
	}
	return session.organizationID, true
}

func (h *Handler) selectSessionOrganization(r *http.Request, organizationID string) bool {
	snapshot, hasSnapshot := viewerSnapshot(r)
	var key [32]byte
	if hasSnapshot {
		if r.Context().Err() != nil {
			return false
		}
		key = snapshot.key
	} else {
		var ok bool
		key, ok = h.sessionHash(r)
		if !ok {
			return false
		}
	}
	h.sessions.mu.Lock()
	defer h.sessions.mu.Unlock()
	session := h.sessions.active[key]
	if session == nil || !h.sessions.now().Before(h.sessions.expires[key]) {
		h.sessions.removeLocked(key)
		return false
	}
	if hasSnapshot && session.ctx != snapshot.baseCtx {
		return false
	}
	if session.organizationID != organizationID {
		session.cancel()
		session.ctx, session.cancel = context.WithCancel(context.Background())
		session.organizationID = organizationID
	}
	return true
}

func (h *Handler) viewerSessionContext(r *http.Request) (context.Context, func(), bool) {
	if _, ok := viewerSnapshot(r); ok {
		if r.Context().Err() != nil {
			return nil, nil, false
		}
		return r.Context(), func() {}, true
	}
	key, ok := h.sessionHash(r)
	if !ok {
		return nil, nil, false
	}
	h.sessions.mu.Lock()
	expires, exists := h.sessions.expires[key]
	session := h.sessions.active[key]
	if !exists || session == nil || !h.sessions.now().Before(expires) {
		h.sessions.removeLocked(key)
		h.sessions.mu.Unlock()
		return nil, nil, false
	}
	base := session.ctx
	h.sessions.mu.Unlock()
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(base, cancel)
	if base.Err() != nil {
		stop()
		cancel()
		return nil, nil, false
	}
	return ctx, func() {
		stop()
		cancel()
	}, true
}

func (s *viewerSessions) removeLocked(key [32]byte) {
	delete(s.expires, key)
	if session := s.active[key]; session != nil {
		delete(s.active, key)
		session.timer.Stop()
		session.cancel()
	}
}

func (s *viewerSessions) expire(key [32]byte, expected *viewerSession) {
	s.mu.Lock()
	if s.active[key] == expected {
		s.removeLocked(key)
	}
	s.mu.Unlock()
}

func (s *viewerSessions) cancelOwner(ownerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, session := range s.active {
		if session.ownerID == ownerID {
			s.removeLocked(key)
		}
	}
}

func (s *viewerSessions) close() {
	s.mu.Lock()
	for key := range s.expires {
		s.removeLocked(key)
	}
	s.mu.Unlock()
}

func (h *Handler) setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	secure := h.cookieSecureForRequest(r)
	legacyName := compat.LegacyPortalCookie
	if secure {
		legacyName = "__Host-" + legacyName
	}
	http.SetCookie(w, &http.Cookie{
		Name: h.cookieNameForRequest(r), Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
	if _, err := r.Cookie(legacyName); err == nil {
		http.SetCookie(w, &http.Cookie{Name: legacyName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	}
}

func (h *Handler) createSession(w http.ResponseWriter, r *http.Request, ownerID string, expires time.Time) bool {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return false
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	key := sha256.Sum256([]byte(token))
	h.sessions.mu.Lock()
	now := h.sessions.now()
	for existing, expiry := range h.sessions.expires {
		if !now.Before(expiry) {
			h.sessions.removeLocked(existing)
		}
	}
	if old, ok := h.sessionHash(r); ok {
		h.sessions.removeLocked(old)
	}
	if !expires.After(now) || !expires.Before(now.Add(sessionLifetime+time.Second)) ||
		len(h.sessions.expires) >= maxViewerSessions {
		h.sessions.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	organizationID := ""
	if h.liveHub != nil {
		organizationID = h.liveHub.initialOrganizationID(ownerID)
	}
	session := &viewerSession{ownerID: ownerID, organizationID: organizationID, ctx: ctx, cancel: cancel}
	h.sessions.expires[key] = expires
	h.sessions.active[key] = session
	session.timer = time.AfterFunc(expires.Sub(now), func() { h.sessions.expire(key, session) })
	h.sessions.mu.Unlock()
	h.setSessionCookie(w, r, token, int(expires.Sub(now).Seconds()))
	return true
}

func (h *Handler) authMutation(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		hubHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
		return false
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || h.liveHub == nil || !h.liveHub.policySnapshot().allows(r) {
		hubHTTPError(w, http.StatusForbidden, "permission_denied", "Host, origin, or request URL is not allowed")
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		hubHTTPError(w, http.StatusUnsupportedMediaType, "invalid_parameters", "JSON required")
		return false
	}
	return true
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.authMutation(w, r) {
		return
	}
	if !h.sessions.allowLogin() {
		w.Header().Set("Retry-After", "60")
		hubHTTPError(w, http.StatusTooManyRequests, "rate_limited", "Too many login attempts")
		return
	}
	var credentials struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !authDecode(w, r, &credentials, "username", "password") {
		return
	}
	username := sha256.Sum256([]byte(credentials.Username))
	password := sha256.Sum256([]byte(credentials.Password))
	localValid := subtle.ConstantTimeCompare(username[:], h.username[:])&subtle.ConstantTimeCompare(password[:], h.password[:]) == 1
	ownerID := h.liveHub.ownerID
	accountValid := false
	if !localValid && len(credentials.Password) <= 72 {
		if account, exists := h.liveHub.store.AccountByEmail(strings.ToLower(strings.TrimSpace(credentials.Username))); exists &&
			account.Status != "disabled" && account.PasswordHash != "" &&
			bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(credentials.Password)) == nil {
			ownerID, accountValid = account.ID, true
		}
	}
	if !localValid && !accountValid {
		hubHTTPError(w, http.StatusUnauthorized, "authentication_required", "Invalid credentials")
		return
	}
	if !h.createSession(w, r, ownerID, h.sessions.now().Add(sessionLifetime)) {
		hubHTTPError(w, http.StatusServiceUnavailable, "session_capacity", "Session capacity reached")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	if !h.authMutation(w, r) {
		return
	}
	if !h.sessions.allowLogin() {
		w.Header().Set("Retry-After", "60")
		hubHTTPError(w, http.StatusTooManyRequests, "rate_limited", "Too many registration or login attempts")
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !hubDecode(w, r, &input, "email", "password") {
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if len(input.Email) > 254 || !validRegistrationPassword(input.Password) {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Email or password does not meet the required bounds")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		hubHTTPError(w, http.StatusServiceUnavailable, "store_unavailable", "Registration could not be completed")
		return
	}
	if _, err := h.liveHub.store.RegisterPendingAccount(input.Email, string(hash)); err != nil {
		var stateError *hubstate.Error
		if errors.As(err, &stateError) && stateError.Code == "email_exists" {
			hubHTTPError(w, http.StatusConflict, stateError.Code, "An account with this email already exists")
			return
		}
		hubStoreError(w, err)
		return
	}
	hubJSON(w, http.StatusCreated, map[string]string{"status": "pending"})
}

func validRegistrationPassword(password string) bool {
	return utf8.ValidString(password) && utf8.RuneCountInString(password) >= 6 && len(password) <= 72
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if !h.authMutation(w, r) {
		return
	}
	ownerID, _ := h.sessionOwnerID(r)
	if key, ok := h.sessionHash(r); ok {
		h.sessions.mu.Lock()
		h.sessions.removeLocked(key)
		h.sessions.mu.Unlock()
	}
	h.setSessionCookie(w, r, "", -1)
	if h.oidc != nil && ownerID != "" && ownerID != h.liveHub.ownerID {
		hubJSON(w, http.StatusOK, map[string]string{"logoutUrl": h.oidcLogoutURL()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
