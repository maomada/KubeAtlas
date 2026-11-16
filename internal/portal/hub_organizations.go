package portal

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func (p *portalHub) organizationList(accountID string) []map[string]any {
	if p.store.IsPlatformOwner(accountID) {
		organizations := p.store.Organizations()
		result := make([]map[string]any, 0, len(organizations))
		for _, organization := range organizations {
			result = append(result, map[string]any{"id": organization.ID, "name": organization.Name, "role": "owner"})
		}
		return result
	}
	memberships := p.store.OrganizationsForAccount(accountID)
	result := make([]map[string]any, 0, len(memberships))
	for _, membership := range memberships {
		organization, ok := p.store.OrganizationByID(membership.OrganizationID)
		if ok {
			result = append(result, map[string]any{"id": organization.ID, "name": organization.Name, "role": membership.Role})
		}
	}
	return result
}

func (p *portalHub) managePlatformRoutes(h *Handler, w http.ResponseWriter, r *http.Request, accountID string, member hubstate.Member, organizationID string) {
	platformOwner := p.store.IsPlatformOwner(accountID)
	switch r.URL.Path {
	case "/api/hub/organizations":
		if r.Method == http.MethodGet {
			hubJSON(w, http.StatusOK, map[string]any{"organizations": p.organizationList(accountID)})
			return
		}
		if !platformOwner {
			hubHTTPError(w, http.StatusForbidden, "permission_denied", "Platform owner access is required")
			return
		}
		var input struct {
			Name          string `json:"name"`
			OwnerMemberID string `json:"ownerMemberId"`
		}
		if !hubMethod(w, r, http.MethodPost) || !hubDecode(w, r, &input, "name", "ownerMemberId") {
			return
		}
		ownerID := input.OwnerMemberID
		if ownerID == hubstate.LocalMemberID {
			ownerID = p.ownerID
		}
		organization, err := p.store.CreateOrganization(input.Name, ownerID)
		if err != nil {
			hubStoreError(w, err)
			return
		}
		hubJSON(w, http.StatusCreated, map[string]any{"id": organization.ID, "name": organization.Name, "role": "owner"})
	case "/api/hub/organization/select":
		var input struct {
			OrganizationID string `json:"organizationId"`
		}
		if !hubMethod(w, r, http.MethodPost) || !hubDecode(w, r, &input, "organizationId") {
			return
		}
		if _, exists := p.store.OrganizationByID(input.OrganizationID); !exists {
			hubStoreError(w, &hubstate.Error{Code: "organization_not_found"})
			return
		}
		allowed := false
		if platformOwner {
			allowed = true
		}
		for _, entry := range p.store.OrganizationsForAccount(accountID) {
			if entry.OrganizationID == input.OrganizationID {
				allowed = true
				break
			}
		}
		if !allowed || input.OrganizationID == "" {
			hubHTTPError(w, http.StatusForbidden, "permission_denied", "Organization access is unavailable")
			return
		}
		if !h.selectSessionOrganization(r, input.OrganizationID) {
			hubHTTPError(w, http.StatusUnauthorized, "authentication_required", "Portal session is unavailable")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "/api/hub/platform/users":
		if !platformOwner {
			hubHTTPError(w, http.StatusForbidden, "permission_denied", "Platform owner access is required")
			return
		}
		if !hubMethod(w, r, http.MethodGet) {
			return
		}
		hubJSON(w, http.StatusOK, map[string]any{"users": p.store.PlatformUsers()})
	case "/api/hub/platform/clusters":
		if !platformOwner {
			hubHTTPError(w, http.StatusForbidden, "permission_denied", "Platform owner access is required")
			return
		}
		if !hubMethod(w, r, http.MethodGet) {
			return
		}
		clusters := make([]hubDirectoryEntry, 0)
		for _, organization := range p.store.Organizations() {
			clusters = append(clusters, p.directory(organization.ID)...)
		}
		hubJSON(w, http.StatusOK, map[string]any{"clusters": clusters})
	default:
		if !platformOwner || !strings.HasPrefix(r.URL.Path, "/api/hub/platform/users/") {
			hubHTTPError(w, http.StatusForbidden, "permission_denied", "Platform owner access is required")
			return
		}
		id, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/hub/platform/users/"))
		if err != nil || id == "" || strings.Contains(id, "/") {
			hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "User ID is invalid")
			return
		}
		if id == hubstate.LocalMemberID {
			id = p.ownerID
		}
		var input struct {
			Status      string                            `json:"status"`
			Memberships []hubstate.OrganizationMembership `json:"memberships"`
		}
		if !hubMethod(w, r, http.MethodPut) || !hubDecode(w, r, &input, "status", "memberships") {
			return
		}
		user, err := p.store.UpdatePlatformUser(id, input.Status, input.Memberships)
		if err != nil {
			hubStoreError(w, err)
			return
		}
		h.sessions.cancelOwner(id)
		p.cancelRequests(id, "")
		hubJSON(w, http.StatusOK, user)
	}
	_ = member
	_ = organizationID
}
