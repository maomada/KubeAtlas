package portal

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func (p *portalHub) localMember() hubstate.Member {
	account, _ := p.store.Account(p.ownerID)
	return hubstate.Member{
		ID: hubstate.LocalMemberID, UserID: p.userID, DisplayName: p.displayName,
		Role: "platformOwner", Status: "active", AuthorizationRevision: account.AuthorizationRevision, ClusterIDs: []string{},
		KubernetesGroups: append([]string{}, p.groups...), CreatedAt: p.startedAt, UpdatedAt: p.startedAt,
	}
}

func publicHubMember(member hubstate.Member) hubstate.Member {
	if member.Role == "platformOwner" {
		member.Role = "owner"
	}
	return member
}

func (p *portalHub) memberForOwner(ownerID, organizationID string) (hubstate.Member, bool) {
	if ownerID == p.ownerID {
		return p.localMember(), true
	}
	if organizationID != "" {
		if member, ok := p.store.MemberForOrganization(organizationID, ownerID); ok {
			return member, true
		}
	}
	account, ok := p.store.Account(ownerID)
	if !ok {
		return hubstate.Member{}, false
	}
	return hubstate.Member{
		ID: account.ID, UserID: account.UserID, DisplayName: account.DisplayName, Status: account.Status,
		AuthorizationRevision: account.AuthorizationRevision, ClusterIDs: []string{}, KubernetesGroups: []string{}, CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt,
	}, true
}

func (p *portalHub) memberClusterIDs(member hubstate.Member, organizationID string) []string {
	if member.Role == "platformOwner" {
		clusters := p.store.ClustersForOrganization(organizationID)
		ids := make([]string, 0, len(clusters))
		for _, cluster := range clusters {
			ids = append(ids, cluster.ID)
		}
		return ids
	}
	ids, err := p.store.MemberClusterIDsForOrganization(organizationID, member.ID)
	if err != nil {
		return []string{}
	}
	return ids
}

func containsScope(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func intersectMemberScope(requested, allowed []string) []string {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}
	result := make([]string, 0, len(requested))
	for _, id := range requested {
		if _, ok := allowedSet[id]; ok {
			result = append(result, id)
		}
	}
	return result
}

func (h *Handler) sessionMember(r *http.Request) (string, hubstate.Member, bool) {
	if h.liveHub == nil {
		return "", hubstate.Member{}, false
	}
	ownerID, ok := h.sessionOwnerID(r)
	if !ok {
		return "", hubstate.Member{}, false
	}
	organizationID, _ := h.sessionOrganizationID(r)
	member, ok := h.liveHub.memberForOwner(ownerID, organizationID)
	return ownerID, member, ok
}

func requireActiveMember(w http.ResponseWriter, member hubstate.Member) bool {
	switch member.Status {
	case "active":
		return true
	case "pending":
		hubHTTPError(w, http.StatusForbidden, "member_pending", "Member access is awaiting approval")
	case "disabled":
		hubHTTPError(w, http.StatusForbidden, "member_disabled", "Member access is disabled")
	default:
		hubHTTPError(w, http.StatusForbidden, "permission_denied", "Member access is unavailable")
	}
	return false
}

func requireHubAdmin(w http.ResponseWriter, member hubstate.Member) bool {
	if member.Role == "owner" || member.Role == "admin" || member.Role == "platformOwner" {
		return true
	}
	hubHTTPError(w, http.StatusForbidden, "permission_denied", "Administrator access is required")
	return false
}

func (p *portalHub) organizationResponse(organizationID string) map[string]any {
	organization, _ := p.store.OrganizationByID(organizationID)
	return map[string]any{
		"id": organization.ID, "name": organization.Name,
		"memberCount": len(p.store.MembersForOrganization(organizationID)),
	}
}

func (p *portalHub) manageOrganization(w http.ResponseWriter, r *http.Request, member hubstate.Member, organizationID string, platformOwner bool) {
	if !requireActiveMember(w, member) {
		return
	}
	if r.Method == http.MethodGet {
		hubJSON(w, http.StatusOK, p.organizationResponse(organizationID))
		return
	}
	if !platformOwner && !requireHubAdmin(w, member) {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !hubMethod(w, r, http.MethodPut) || !hubDecode(w, r, &input, "name") {
		return
	}
	if _, err := p.store.SetOrganizationNameFor(organizationID, input.Name); err != nil {
		hubStoreError(w, err)
		return
	}
	hubJSON(w, http.StatusOK, p.organizationResponse(organizationID))
}

func (p *portalHub) manageMembers(w http.ResponseWriter, r *http.Request, member hubstate.Member, organizationID string) {
	if !requireActiveMember(w, member) || !requireHubAdmin(w, member) || !hubMethod(w, r, http.MethodGet) {
		return
	}
	members := p.store.MembersForOrganization(organizationID)
	for i := range members {
		if members[i].ID == p.ownerID {
			members[i].ID = hubstate.LocalMemberID
		}
	}
	hubJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (p *portalHub) updateMember(h *Handler, w http.ResponseWriter, r *http.Request, actor hubstate.Member, organizationID string) {
	if !requireActiveMember(w, actor) || !requireHubAdmin(w, actor) {
		return
	}
	rawID := strings.TrimPrefix(r.URL.Path, "/api/hub/members/")
	id, err := url.PathUnescape(rawID)
	if err != nil || id == "" || strings.Contains(id, "/") {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Member ID is invalid")
		return
	}
	if id == hubstate.LocalMemberID || id == p.ownerID {
		hubHTTPError(w, http.StatusForbidden, "permission_denied", "The local owner cannot be changed")
		return
	}
	if r.Method == http.MethodDelete {
		if !hubDecode(w, r, &struct{}{}) {
			return
		}
		if _, err := p.store.RemoveOrganizationMember(organizationID, id); err != nil {
			hubStoreError(w, err)
			return
		}
		h.sessions.cancelOwner(id)
		p.cancelRequests(id, organizationID)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input struct {
		Role string `json:"role"`
	}
	if !hubMethod(w, r, http.MethodPut) || !hubDecode(w, r, &input, "role") {
		return
	}
	member, changed, err := p.store.UpdateOrganizationMemberRole(organizationID, id, input.Role)
	if err != nil {
		hubStoreError(w, err)
		return
	}
	if changed {
		h.sessions.cancelOwner(id)
		p.cancelRequests(id, organizationID)
	}
	hubJSON(w, http.StatusOK, map[string]any{"member": member})
}

func (p *portalHub) serveSSO(w http.ResponseWriter, r *http.Request, member hubstate.Member) {
	if !requireActiveMember(w, member) || !hubMethod(w, r, http.MethodGet) {
		return
	}
	response := map[string]any{
		"enabled": p.oidcConfig != nil, "directorySync": false, "backchannelLogout": false,
		"provisioning": "approval",
	}
	if p.oidcConfig != nil {
		response["issuer"] = p.oidcConfig.Issuer
		response["clientId"] = p.oidcConfig.ClientID
		response["redirectUrl"] = p.oidcConfig.RedirectURL
	}
	hubJSON(w, http.StatusOK, response)
}
