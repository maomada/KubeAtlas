package portal

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"unicode"
)

const maxStaticAssetBytes = 8 << 20

type Credentials struct {
	Username             string
	Password             string
	AllowInsecureCookies bool
	HTTPIPOrigin         string
	MCPAllowedHosts      []string
	Hub                  *HubConfig
	OIDC                 *OIDCConfig
}

type Handler struct {
	static        fs.FS
	username      [32]byte
	password      [32]byte
	sessions      *viewerSessions
	secureCookies bool
	httpIPOrigin  string
	liveHub       *portalHub
	oidc          *oidcAuthenticator
}

func NewHandler(credentials Credentials, static fs.FS) (*Handler, error) {
	if credentials.Hub == nil {
		return nil, errors.New("Hub configuration is required")
	}
	if !validUsername(credentials.Username) || len(credentials.Username) > 256 || len(credentials.Password) < 16 || len(credentials.Password) > 4096 {
		return nil, errors.New("Portal owner username must be valid (1-256 bytes) and password must contain 16-4096 bytes")
	}
	httpIPOrigin, err := normalizeHTTPIPOrigin(credentials.HTTPIPOrigin)
	if err != nil {
		return nil, err
	}
	credentials.HTTPIPOrigin = httpIPOrigin
	handler := &Handler{
		static:   static,
		username: sha256.Sum256([]byte(credentials.Username)),
		password: sha256.Sum256([]byte(credentials.Password)),
		sessions: newViewerSessions(), secureCookies: !credentials.AllowInsecureCookies, httpIPOrigin: httpIPOrigin,
	}
	handler.liveHub, err = newPortalHub(credentials)
	if err != nil {
		handler.Close()
		return nil, err
	}
	if credentials.OIDC != nil {
		secureOIDCCookie := handler.secureCookies || strings.HasPrefix(strings.ToLower(credentials.OIDC.RedirectURL), "https://")
		handler.oidc, err = newOIDCAuthenticator(*credentials.OIDC, secureOIDCCookie)
		if err != nil {
			handler.Close()
			return nil, err
		}
	}
	return handler, nil
}

func (h *Handler) Close() {
	if h != nil && h.sessions != nil {
		h.sessions.close()
	}
	if h != nil && h.liveHub != nil {
		_ = h.liveHub.transport.Close()
		if h.liveHub.catalog != nil {
			_ = h.liveHub.catalog.Close()
		}
	}
	if h != nil && h.oidc != nil && h.oidc.transport != nil {
		h.oidc.transport.CloseIdleConnections()
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	if r.URL.Path == "/healthz" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var release func()
	r, release = h.bindViewerSession(r)
	defer release()

	if h.serveHub(w, r) {
		return
	}

	switch {
	case r.URL.Path == "/api/auth/register":
		h.register(w, r)
	case r.URL.Path == "/api/auth/login":
		h.login(w, r)
	case r.URL.Path == "/api/auth/providers":
		if r.URL.RawQuery != "" || r.URL.ForceQuery || !h.liveHub.policySnapshot().allows(r) {
			hubHTTPError(w, http.StatusForbidden, "permission_denied", "Host or request URL is not allowed")
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		providers := map[string]any{"local": true, "oidc": map[string]any{"enabled": h.oidc != nil}, "registrationEnabled": true}
		if h.oidc != nil {
			providers["oidc"] = map[string]any{"enabled": true, "loginUrl": "/api/auth/oidc/login"}
		}
		hubJSON(w, http.StatusOK, providers)
	case r.URL.Path == "/api/auth/oidc/login":
		h.oidcLogin(w, r)
	case r.URL.Path == "/api/auth/oidc/callback":
		h.oidcCallback(w, r)
	case r.URL.Path == "/api/auth/logout":
		h.logout(w, r)
	case r.URL.Path == "/api/auth/session":
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.URL.ForceQuery || !h.liveHub.policySnapshot().allows(r) {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if h.requireViewer(w, r) {
			w.WriteHeader(http.StatusNoContent)
		}
	case r.URL.Path == "/" || r.URL.Path == "/login" || r.URL.Path == "/register" || r.URL.Path == "/mcp-access":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		loggedIn := h.hasViewerSession(r)
		if r.URL.Path == "/mcp-access" && !loggedIn {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if (r.URL.Path == "/login" || r.URL.Path == "/register") && loggedIn {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		h.serveFile(w, r, "portal.html")
	case isHubPageRoute(r):
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/login" && !h.requireViewer(w, r) {
			return
		}
		h.serveFile(w, r, "portal.html")
	case strings.HasPrefix(r.URL.Path, "/assets/"):
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if !strings.HasPrefix(name, "assets/") || name == "assets" {
			http.NotFound(w, r)
			return
		}
		h.serveFile(w, r, name)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	if h.static == nil {
		http.Error(w, "portal web assets are unavailable; build the portal frontend and embed it", http.StatusServiceUnavailable)
		return
	}
	file, err := h.static.Open(name)
	if err != nil {
		http.Error(w, "portal web assets are unavailable; build the portal frontend and embed it", http.StatusServiceUnavailable)
		return
	}
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		_ = file.Close()
		http.Error(w, "portal web assets are unavailable; build the portal frontend and embed it", http.StatusServiceUnavailable)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxStaticAssetBytes+1))
	if err != nil || len(content) > maxStaticAssetBytes {
		http.Error(w, "portal web asset is unavailable or too large", http.StatusServiceUnavailable)
		return
	}
	if name == "portal.html" {
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", mimeType(name))
	}
	http.ServeContent(w, r, path.Base(name), info.ModTime(), bytes.NewReader(content))
}

func mimeType(name string) string {
	switch path.Ext(name) {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".woff", ".woff2":
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}

func (h *Handler) requireViewer(w http.ResponseWriter, r *http.Request) bool {
	if !h.hasViewerSession(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func validUsername(value string) bool {
	if value == "" || strings.Contains(value, ":") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func sameSiteRequest(r *http.Request) bool {
	value := strings.ToLower(r.Header.Get("Sec-Fetch-Site"))
	return value != "cross-site" && value != "same-site"
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
}
