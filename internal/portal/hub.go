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
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/maomada/KubeAtlas/internal/hub"
	"github.com/maomada/KubeAtlas/internal/hubartifacts"
	"github.com/maomada/KubeAtlas/internal/hubstate"
	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
)

type HubConfig struct {
	Store      *hubstate.Store
	Artifacts  *hubartifacts.Catalog
	Groups     []string
	MCPFactory func(*hub.Hub, *hubstate.Store) (http.Handler, error)
}

type portalHub struct {
	store              *hubstate.Store
	transport          *hub.Hub
	policy             *hubRequestPolicy
	ownerID            string
	userID             string
	groups             []string
	displayName        string
	startedAt          time.Time
	httpIPOrigin       string
	oidcConfig         *OIDCConfig
	mcp                http.Handler
	catalog            *hubartifacts.Catalog
	requestsMu         sync.Mutex
	requests           map[hubRequestScope]map[uint64]context.CancelFunc
	nextRequest        uint64
	enrollmentMu       sync.Mutex
	enrollments        map[string]*hubEnrollment
	enrollmentAttempts map[string][]time.Time
}

type hubRequestScope struct{ ownerID, organizationID, credentialID string }

func (p *portalHub) policySnapshot() *hubRequestPolicy {
	connections := p.store.Connections()
	return p.policy.withOrigins(connections.LANURL, connections.PublicURL, p.httpIPOrigin)
}

func newPortalHub(credentials Credentials) (*portalHub, error) {
	cfg := credentials.Hub
	if cfg.Store == nil {
		return nil, errors.New("Hub state store is required")
	}
	digest := sha256.Sum256([]byte(credentials.Username))
	id := hex.EncodeToString(digest[:])
	p := &portalHub{store: cfg.Store, catalog: cfg.Artifacts, ownerID: "portal:" + id, userID: "kubeatlas:portal:" + id, groups: slices.Clone(cfg.Groups), displayName: credentials.Username, startedAt: time.Now().UTC(), httpIPOrigin: credentials.HTTPIPOrigin, oidcConfig: credentials.OIDC,
		requests: map[hubRequestScope]map[uint64]context.CancelFunc{}, enrollments: map[string]*hubEnrollment{}, enrollmentAttempts: map[string][]time.Time{}}
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
	if err := p.store.EnsurePlatformOwner(p.ownerID, p.userID, p.displayName); err != nil {
		return nil, err
	}
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

func (p *portalHub) activeRequest(r *http.Request, ownerID, organizationID string) (*http.Request, func()) {
	return p.activeScopedRequest(r, hubRequestScope{ownerID: ownerID, organizationID: organizationID})
}

func (p *portalHub) activeCredentialRequest(r *http.Request, ownerID, organizationID, credentialID string) (*http.Request, func()) {
	if snapshot, ok := viewerSnapshot(r); ok {
		r = r.WithContext(snapshot.requestContext)
	}
	return p.activeScopedRequest(r, hubRequestScope{ownerID: ownerID, organizationID: organizationID, credentialID: credentialID})
}

func (p *portalHub) activeScopedRequest(r *http.Request, scope hubRequestScope) (*http.Request, func()) {
	ctx, cancel := context.WithCancel(r.Context())
	p.requestsMu.Lock()
	p.nextRequest++
	id := p.nextRequest
	if p.requests[scope] == nil {
		p.requests[scope] = map[uint64]context.CancelFunc{}
	}
	p.requests[scope][id] = cancel
	p.requestsMu.Unlock()
	return r.Clone(ctx), func() {
		cancel()
		p.requestsMu.Lock()
		delete(p.requests[scope], id)
		if len(p.requests[scope]) == 0 {
			delete(p.requests, scope)
		}
		p.requestsMu.Unlock()
	}
}

func (p *portalHub) cancelCredentialRequests(ownerID, organizationID, credentialID string) {
	p.requestsMu.Lock()
	defer p.requestsMu.Unlock()
	for scope, requests := range p.requests {
		if scope.ownerID != ownerID || scope.organizationID != organizationID || scope.credentialID != credentialID {
			continue
		}
		for _, cancel := range requests {
			cancel()
		}
	}
}

func (p *portalHub) cancelRequests(ownerID, organizationID string) {
	p.requestsMu.Lock()
	defer p.requestsMu.Unlock()
	for scope, requests := range p.requests {
		if ownerID != "" && scope.ownerID != ownerID || organizationID != "" && scope.organizationID != organizationID {
			continue
		}
		for _, cancel := range requests {
			cancel()
		}
	}
}

func (p *portalHub) initialOrganizationID(ownerID string) string {
	organizations := p.store.OrganizationsForAccount(ownerID)
	if len(organizations) == 0 {
		return ""
	}
	return organizations[0].OrganizationID
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
	artifactRoute := r.URL.Path == "/v2/" || strings.HasPrefix(r.URL.Path, "/v2/") || strings.HasPrefix(r.URL.Path, "/downloads/agent/")
	if p == nil || !(artifactRoute || r.URL.Path == "/agent" || r.URL.Path == "/mcp" || r.URL.Path == "/api/hub" || strings.HasPrefix(r.URL.Path, "/api/hub/") || r.URL.Path == "/api/mcp/credentials" || strings.HasPrefix(r.URL.Path, "/api/mcp/credentials/")) {
		return false
	}
	w.Header().Set("X-Request-ID", rand.Text())
	policy := p.policySnapshot()
	if !policy.allows(r) || artifactRoute && len(r.Header.Values("Sec-Fetch-Site")) > 0 && !policy.allowsBrowser(r) {
		hubHTTPError(w, http.StatusForbidden, "permission_denied", "Host or browser origin is not allowed")
		return true
	}
	if artifactRoute {
		if p.catalog == nil || !p.catalog.ServeHTTP(w, r) {
			http.NotFound(w, r)
		}
		return true
	}
	if r.URL.Path == "/agent" {
		p.transport.ServeHTTP(w, r)
		return true
	}
	if r.URL.Path == "/api/hub/enrollments" {
		p.createEnrollment(w, r)
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/api/hub/enrollments/") && strings.HasSuffix(r.URL.Path, "/claim") {
		p.claimEnrollment(w, r)
		return true
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Query parameters are not supported")
		return true
	}
	if r.URL.Path == "/mcp" || r.URL.Path == "/api/mcp/credentials/current" || r.URL.Path == "/api/mcp/credentials/refresh" {
		p.serveBearer(w, r)
		return true
	}
	if len(r.Header.Values("Authorization")) != 0 || !h.hasViewerSession(r) {
		hubHTTPError(w, http.StatusUnauthorized, "authentication_required", "A Portal login session is required; bearer credentials cannot manage the Hub")
		return true
	}
	ownerID, member, ok := h.sessionMember(r)
	if !ok {
		hubHTTPError(w, http.StatusUnauthorized, "authentication_required", "A current Portal member session is required")
		return true
	}
	organizationID, _ := h.sessionOrganizationID(r)
	if !p.store.IsPlatformOwner(ownerID) && organizationID != "" && member.Role == "" {
		h.selectSessionOrganization(r, "")
		organizationID = ""
	}
	if member.Status == "pending" || organizationID == "" && !p.store.IsPlatformOwner(ownerID) {
		if r.URL.Path == "/api/hub" && hubMethod(w, r, http.MethodGet) {
			organizations := p.organizationList(ownerID)
			hubJSON(w, http.StatusOK, map[string]any{"enabled": true, "source": "live", "mcpReady": p.mcp != nil,
				"userId": member.UserID, "groups": []string{}, "member": publicHubMember(member), "platformOwner": p.store.IsPlatformOwner(ownerID),
				"activeOrganizationId": nil, "organizations": organizations})
		} else if member.Status == "active" && organizationID == "" && r.URL.Path == "/api/hub/organizations" && hubMethod(w, r, http.MethodGet) {
			hubJSON(w, http.StatusOK, map[string]any{"organizations": p.organizationList(ownerID)})
		} else if member.Status == "active" && organizationID == "" && r.URL.Path == "/api/hub/organization/select" {
			p.managePlatformRoutes(h, w, r, ownerID, member, organizationID)
		} else {
			code, message := "member_pending", "Member access is awaiting organization approval"
			if member.Status == "active" {
				code, message = "permission_denied", "An active organization is required"
			}
			hubHTTPError(w, http.StatusForbidden, code, message)
		}
		return true
	}
	switch {
	case r.URL.Path == "/api/hub":
		if hubMethod(w, r, http.MethodGet) {
			activeOrganizationID := any(organizationID)
			if organizationID == "" {
				activeOrganizationID = nil
			}
			hubJSON(w, http.StatusOK, map[string]any{"enabled": true, "source": "live", "mcpReady": p.mcp != nil,
				"userId": member.UserID, "groups": append([]string{}, member.KubernetesGroups...), "member": publicHubMember(member),
				"platformOwner": p.store.IsPlatformOwner(ownerID), "activeOrganizationId": activeOrganizationID,
				"organizations": p.organizationList(ownerID)})
		}
	case r.URL.Path == "/api/hub/organizations" || r.URL.Path == "/api/hub/organization/select" || r.URL.Path == "/api/hub/platform/users" || strings.HasPrefix(r.URL.Path, "/api/hub/platform/users/") || r.URL.Path == "/api/hub/platform/clusters":
		p.managePlatformRoutes(h, w, r, ownerID, member, organizationID)
	case strings.HasPrefix(r.URL.Path, "/api/hub/enrollments"):
		if requireActiveMember(w, member) {
			p.manageEnrollments(w, r, ownerID, member, organizationID)
		}
	case strings.HasPrefix(r.URL.Path, "/api/mcp/credentials"):
		if !requireActiveMember(w, member) {
			return true
		}
		p.manageCredentials(w, r, ownerID, member, organizationID)
	case r.URL.Path == "/api/hub/organization":
		p.manageOrganization(w, r, member, organizationID, p.store.IsPlatformOwner(ownerID))
	case r.URL.Path == "/api/hub/connections":
		if !requireActiveMember(w, member) || r.Method == http.MethodPut && !p.store.IsPlatformOwner(ownerID) {
			hubHTTPError(w, http.StatusForbidden, "permission_denied", "Platform owner access is required")
			return true
		}
		p.manageConnections(w, r, member)
	case r.URL.Path == "/api/hub/members":
		p.manageMembers(w, r, member, organizationID)
	case strings.HasPrefix(r.URL.Path, "/api/hub/members/"):
		p.updateMember(h, w, r, member, organizationID)
	case r.URL.Path == "/api/hub/sso":
		p.serveSSO(w, r, member)
	case r.URL.Path == "/api/hub/clusters" || strings.HasPrefix(r.URL.Path, "/api/hub/clusters/"):
		if !requireActiveMember(w, member) {
			return true
		}
		p.manageClusters(w, r, member, organizationID)
	case r.URL.Path == "/api/hub/groups" || strings.HasPrefix(r.URL.Path, "/api/hub/groups/"):
		if !requireActiveMember(w, member) {
			return true
		}
		p.manageGroups(w, r, member, organizationID)
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
	scope, err := p.store.CredentialScope(token)
	if err != nil {
		hubStoreError(w, err)
		return
	}
	ownerID, organizationID := scope.OwnerID, scope.OrganizationID
	var requestRelease func()
	if r.URL.Path == "/mcp" {
		r, requestRelease = p.activeCredentialRequest(r, ownerID, organizationID, scope.ID)
		defer requestRelease()
		if r.Context().Err() != nil {
			hubHTTPError(w, http.StatusUnauthorized, "credential_invalid", "Credential request was cancelled")
			return
		}
	}
	member, ok := p.memberForOwner(ownerID, organizationID)
	if !ok {
		hubHTTPError(w, http.StatusUnauthorized, "credential_invalid", "Credential owner is not available")
		return
	}
	if !p.store.IsPlatformOwner(ownerID) && (member.Status != "active" || member.Role == "") {
		w.Header().Set("WWW-Authenticate", `Bearer realm="kubeatlas-hub", error="invalid_token"`)
		hubHTTPError(w, http.StatusUnauthorized, "credential_invalid", "Credential owner is no longer authorized")
		return
	}
	if !requireActiveMember(w, member) {
		return
	}
	credential, err := p.store.AuthenticateForOrganization(token, ownerID, organizationID)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="kubeatlas-hub", error="invalid_token"`)
		hubStoreError(w, err)
		return
	}
	if r.Context().Err() != nil {
		hubHTTPError(w, http.StatusUnauthorized, "credential_revoked", "Credential request was cancelled")
		return
	}
	var expiryTimer *time.Timer
	if requestRelease != nil {
		delay := time.Until(credential.ExpiresAt)
		if delay <= 0 {
			p.cancelCredentialRequests(ownerID, organizationID, credential.ID)
		} else {
			expiryTimer = time.AfterFunc(delay, func() { p.cancelCredentialRequests(ownerID, organizationID, credential.ID) })
		}
		if expiryTimer != nil {
			defer expiryTimer.Stop()
		}
	}
	allowed := p.memberClusterIDs(member, organizationID)
	credential.ClusterIDs = intersectMemberScope(credential.ClusterIDs, allowed)
	if len(credential.ClusterIDs) == 0 {
		hubHTTPError(w, http.StatusForbidden, "permission_denied", "Credential has no current cluster access")
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
		var issued hubstate.IssuedCredential
		if p.store.IsPlatformOwner(ownerID) {
			issued, err = p.store.RefreshForOrganization(organizationID, token, ownerID, input.TTLSeconds)
		} else {
			issued, err = p.store.RefreshForMemberInOrganization(organizationID, token, ownerID, input.TTLSeconds)
		}
		if err != nil {
			hubStoreError(w, err)
			return
		}
		p.cancelCredentialRequests(ownerID, organizationID, credential.ID)
		hubJSON(w, http.StatusOK, issued)
	case "/mcp":
		if p.mcp == nil {
			hubHTTPError(w, http.StatusServiceUnavailable, "live_mcp_unavailable", "Live MCP is not configured")
			return
		}
		groups := p.principalGroups(ownerID, organizationID, member)
		principal := hub.Principal{CredentialID: credential.ID, AccountID: ownerID, OrganizationID: organizationID, UserID: member.UserID,
			Groups: groups, ClusterIDs: credential.ClusterIDs, ReadOnly: credential.ReadOnly || member.Role == "viewer"}
		request := r.Clone(hub.ContextWithPrincipal(r.Context(), principal))
		request.Header.Del("Authorization")
		request.Header.Del("Cookie")
		request.Header.Del("Proxy-Authorization")
		p.mcp.ServeHTTP(w, request)
	}
}

func (p *portalHub) principalGroups(ownerID, organizationID string, member hubstate.Member) []string {
	groups := append([]string{}, member.KubernetesGroups...)
	if !p.store.IsPlatformOwner(ownerID) {
		if roleGroup := p.store.RoleGroupForMember(organizationID, ownerID); roleGroup != "" {
			groups = append(groups, roleGroup)
		}
	}
	slices.Sort(groups)
	return slices.Compact(groups)
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
	if w.Header().Get("X-Request-ID") == "" {
		w.Header().Set("X-Request-ID", rand.Text())
	}
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
		case "permission_denied", "member_pending", "member_disabled":
			status = http.StatusForbidden
		case "conflict", "email_exists", "capacity_exceeded", "last_owner", "immutable_account":
			status = http.StatusConflict
		case "cluster_not_found", "member_not_found", "account_not_found", "organization_not_found":
			status = http.StatusNotFound
		case "credential_invalid", "credential_expired", "credential_revoked", "credential_refresh_expired":
			status = http.StatusUnauthorized
		}
	}
	hubHTTPError(w, status, code, strings.ReplaceAll(code, "_", " "))
}
