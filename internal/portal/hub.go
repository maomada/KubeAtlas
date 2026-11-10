package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/maomada/KubeAtlas/internal/hub"
	"github.com/maomada/KubeAtlas/internal/hubstate"
	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
)

type HubConfig struct {
	Store      *hubstate.Store
	Groups     []string
	MCPFactory func(*hub.Hub, *hubstate.Store) (http.Handler, error)
}

type portalHub struct {
	store     *hubstate.Store
	transport *hub.Hub
	policy    *hubRequestPolicy
	ownerID   string
	userID    string
	groups    []string
	mcp       http.Handler
}

func newPortalHub(credentials Credentials) (*portalHub, error) {
	cfg := credentials.Hub
	if cfg.Store == nil {
		return nil, errors.New("Hub state store is required")
	}
	digest := sha256.Sum256([]byte(credentials.Username))
	id := hex.EncodeToString(digest[:])
	p := &portalHub{store: cfg.Store, ownerID: "portal:" + id, userID: "kubeatlas:portal:" + id, groups: slices.Clone(cfg.Groups)}
	if len(p.groups) > 128 || !pkgauth.ForwardedIdentityAllowed(p.userID, p.groups, true) {
		return nil, errors.New("Hub identity groups are invalid")
	}
	total := 0
	for _, group := range p.groups {
		total += len(group) + 1
		if group == "" || len(group) > 256 || !utf8.ValidString(group) || strings.TrimSpace(group) != group || strings.ContainsRune(group, ',') || strings.ContainsFunc(group, unicode.IsControl) || total > 8192 {
			return nil, errors.New("Hub identity groups are invalid")
		}
	}
	slices.Sort(p.groups)
	p.groups = slices.Compact(p.groups)
	var err error
	p.policy, err = newHubRequestPolicy(credentials.MCPAllowedHosts)
	if err != nil {
		return nil, err
	}
	p.transport, err = hub.New(hub.Config{AuthenticateAgent: func(_ context.Context, token, claimedID string) (hub.AgentIdentity, error) {
		cluster, err := p.store.AuthenticateAgent(token, claimedID)
		return hub.AgentIdentity{ClusterID: cluster.ID, Name: cluster.Name}, err
	}})
	if err != nil {
		return nil, err
	}
	if cfg.MCPFactory != nil {
		p.mcp, err = cfg.MCPFactory(p.transport, p.store)
		if err != nil || p.mcp == nil {
			_ = p.transport.Close()
			return nil, errors.New("Hub MCP handler initialization failed")
		}
	}
	return p, nil
}

func (h *Handler) serveHub(w http.ResponseWriter, r *http.Request) bool {
	p := h.liveHub
	if isClusterAPICandidate(r.URL.Path) {
		w.Header().Set("X-Request-ID", rand.Text())
		if p == nil {
			http.NotFound(w, r)
			return true
		}
		p.serveBrowserAPI(h, w, r)
		return true
	}
	if p == nil || !(r.URL.Path == "/agent" || r.URL.Path == "/mcp-fleet" || r.URL.Path == "/api/hub" || strings.HasPrefix(r.URL.Path, "/api/hub/") || r.URL.Path == "/api/mcp/credentials" || strings.HasPrefix(r.URL.Path, "/api/mcp/credentials/")) {
		return false
	}
	w.Header().Set("X-Request-ID", rand.Text())
	if !p.policy.allows(r) {
		hubHTTPError(w, http.StatusForbidden, "permission_denied", "Host or browser origin is not allowed")
		return true
	}
	if r.URL.Path == "/agent" {
		p.transport.ServeHTTP(w, r)
		return true
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Query parameters are not supported")
		return true
	}
	if r.URL.Path == "/mcp-fleet" || r.URL.Path == "/api/mcp/credentials/current" || r.URL.Path == "/api/mcp/credentials/refresh" {
		p.serveBearer(w, r)
		return true
	}
	if len(r.Header.Values("Authorization")) != 0 || !h.hasViewerSession(r) {
		hubHTTPError(w, http.StatusUnauthorized, "authentication_required", "A Portal login session is required; bearer credentials cannot manage the Hub")
		return true
	}
	switch {
	case r.URL.Path == "/api/hub":
		if hubMethod(w, r, http.MethodGet) {
			hubJSON(w, http.StatusOK, map[string]any{"enabled": true, "source": "live", "mcpReady": p.mcp != nil, "userId": p.userID, "groups": append([]string{}, p.groups...)})
		}
	case strings.HasPrefix(r.URL.Path, "/api/mcp/credentials"):
		p.manageCredentials(w, r)
	case r.URL.Path == "/api/hub/clusters" || strings.HasPrefix(r.URL.Path, "/api/hub/clusters/"):
		p.manageClusters(w, r)
	case r.URL.Path == "/api/hub/groups" || strings.HasPrefix(r.URL.Path, "/api/hub/groups/"):
		p.manageGroups(w, r)
	default:
		hubHTTPError(w, http.StatusNotFound, "not_found", "Hub route not found")
	}
	return true
}

func (p *portalHub) serveBearer(w http.ResponseWriter, r *http.Request) {
	values := r.Header.Values("Authorization")
	var token string
	if len(values) == 1 && len(values[0]) <= 4103 {
		scheme, value, found := strings.Cut(values[0], " ")
		if found && strings.EqualFold(scheme, "Bearer") && value != "" && !strings.ContainsFunc(value, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }) {
			token = value
		}
	}
	if token == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="kubeatlas-hub"`)
		hubHTTPError(w, http.StatusUnauthorized, "credential_invalid", "A single MCP Bearer credential is required")
		return
	}
	credential, err := p.store.Authenticate(token, p.ownerID)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="kubeatlas-hub", error="invalid_token"`)
		hubStoreError(w, err)
		return
	}
	switch r.URL.Path {
	case "/api/mcp/credentials/current":
		if hubMethod(w, r, http.MethodGet) {
			hubJSON(w, http.StatusOK, map[string]any{"credential": credential, "status": "active", "serverTime": time.Now().UTC()})
		}
	case "/api/mcp/credentials/refresh":
		var input struct {
			TTLSeconds int `json:"ttlSeconds"`
		}
		if !hubMethod(w, r, http.MethodPost) || !hubDecode(w, r, &input, "ttlSeconds") {
			return
		}
		issued, err := p.store.Refresh(token, p.ownerID, input.TTLSeconds)
		if err != nil {
			hubStoreError(w, err)
			return
		}
		hubJSON(w, http.StatusOK, issued)
	case "/mcp-fleet":
		if p.mcp == nil {
			hubHTTPError(w, http.StatusServiceUnavailable, "live_mcp_unavailable", "Live MCP is not configured; no snapshot fallback is used")
			return
		}
		principal := hub.Principal{CredentialID: credential.ID, UserID: p.userID, Groups: p.groups, ClusterIDs: credential.ClusterIDs, ReadOnly: credential.ReadOnly}
		request := r.Clone(hub.ContextWithPrincipal(r.Context(), principal))
		request.Header.Del("Authorization")
		request.Header.Del("Cookie")
		request.Header.Del("Proxy-Authorization")
		p.mcp.ServeHTTP(w, request)
	}
}

func hubMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	hubHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
	return false
}

func hubJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func hubHTTPError(w http.ResponseWriter, status int, code, message string) {
	hubJSON(w, status, map[string]any{"error_code": code, "message": message, "requestId": w.Header().Get("X-Request-ID"), "retryable": status == http.StatusServiceUnavailable})
}

func hubStoreError(w http.ResponseWriter, err error) {
	code, status := "store_unavailable", http.StatusServiceUnavailable
	var stateError *hubstate.Error
	if errors.As(err, &stateError) {
		code = stateError.Code
		switch code {
		case "invalid_parameters", "invalid_scope":
			status = http.StatusBadRequest
		case "conflict":
			status = http.StatusConflict
		case "cluster_not_found":
			status = http.StatusNotFound
		case "credential_invalid", "credential_expired", "credential_revoked", "credential_refresh_expired":
			status = http.StatusUnauthorized
		}
	}
	hubHTTPError(w, status, code, strings.ReplaceAll(code, "_", " "))
}
