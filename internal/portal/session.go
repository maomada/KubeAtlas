package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sync"
	"time"
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
	ownerID string
	ctx     context.Context
	cancel  context.CancelFunc
	timer   *time.Timer
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
		return "__Host-radar_portal_session"
	}
	return "radar_portal_session"
}

func (h *Handler) sessionHash(r *http.Request) ([32]byte, bool) {
	cookie, err := r.Cookie(h.cookieName())
	if err != nil || len(cookie.Value) != 43 {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(cookie.Value)), true
}

func (h *Handler) hasViewerSession(r *http.Request) bool {
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
	if h.liveHub != nil && ownerID != "" && ownerID != h.liveHub.ownerID {
		member, exists := h.liveHub.store.Member(ownerID)
		if !exists || member.Status == "disabled" {
			h.sessions.mu.Lock()
			h.sessions.removeLocked(key)
			h.sessions.mu.Unlock()
			return false
		}
	}
	return true
}

func (h *Handler) sessionOwnerID(r *http.Request) (string, bool) {
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

func (h *Handler) viewerSessionContext(r *http.Request) (context.Context, func(), bool) {
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

func (h *Handler) setSessionCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: h.cookieName(), Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteStrictMode,
	})
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
	session := &viewerSession{ownerID: ownerID, ctx: ctx, cancel: cancel}
	h.sessions.expires[key] = expires
	h.sessions.active[key] = session
	session.timer = time.AfterFunc(expires.Sub(now), func() { h.sessions.expire(key, session) })
	h.sessions.mu.Unlock()
	h.setSessionCookie(w, token, int(expires.Sub(now).Seconds()))
	return true
}

func authMutation(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !sameSiteRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host != r.Host || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return false
		}
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !authMutation(w, r) {
		return
	}
	if !h.sessions.allowLogin() {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var credentials struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if decoder.Decode(&credentials) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid login request", http.StatusBadRequest)
		return
	}
	username := sha256.Sum256([]byte(credentials.Username))
	password := sha256.Sum256([]byte(credentials.Password))
	if subtle.ConstantTimeCompare(username[:], h.username[:])&subtle.ConstantTimeCompare(password[:], h.password[:]) != 1 {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	ownerID := ""
	if h.liveHub != nil {
		ownerID = h.liveHub.ownerID
	}
	if !h.createSession(w, r, ownerID, h.sessions.now().Add(sessionLifetime)) {
		http.Error(w, "session capacity reached", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if !authMutation(w, r) {
		return
	}
	ownerID, _ := h.sessionOwnerID(r)
	if key, ok := h.sessionHash(r); ok {
		h.sessions.mu.Lock()
		h.sessions.removeLocked(key)
		h.sessions.mu.Unlock()
	}
	h.setSessionCookie(w, "", -1)
	if h.oidc != nil && ownerID != "" && ownerID != h.liveHub.ownerID {
		hubJSON(w, http.StatusOK, map[string]string{"logoutUrl": h.oidcLogoutURL()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
