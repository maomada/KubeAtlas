package portal

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func (p *portalHub) localMember() hubstate.Member {
	return hubstate.Member{
		ID: hubstate.LocalMemberID, UserID: p.userID, DisplayName: p.displayName,
		Role: "owner", Status: "active", ClusterIDs: []string{},
		KubernetesGroups: append([]string{}, p.groups...), CreatedAt: p.startedAt, UpdatedAt: p.startedAt,
	}
}

func (p *portalHub) memberForOwner(ownerID string) (hubstate.Member, bool) {
	if ownerID == p.ownerID {
		return p.localMember(), true
	}
	member, ok := p.store.Member(ownerID)
	return member, ok
}

func (p *portalHub) memberClusterIDs(member hubstate.Member) []string {
	if member.Role == "owner" || member.Role == "admin" {
		clusters := p.store.Clusters()
		ids := make([]string, 0, len(clusters))
		for _, cluster := range clusters {
			ids = append(ids, cluster.ID)
		}
		return ids
	}
	return append([]string{}, member.ClusterIDs...)
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
	member, ok := h.liveHub.memberForOwner(ownerID)
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
	if member.Role == "owner" || member.Role == "admin" {
		return true
	}
	hubHTTPError(w, http.StatusForbidden, "permission_denied", "Administrator access is required")
	return false
}

func (p *portalHub) organizationResponse() map[string]any {
	organization := p.store.Organization()
	return map[string]any{
		"id": organization.ID, "name": organization.Name,
		"memberCount": p.store.ActiveMemberCount() + 1,
	}
}

func (p *portalHub) manageOrganization(w http.ResponseWriter, r *http.Request, member hubstate.Member) {
	if !requireActiveMember(w, member) {
		return
	}
	if r.Method == http.MethodGet {
		hubJSON(w, http.StatusOK, p.organizationResponse())
		return
	}
	if !requireHubAdmin(w, member) {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !hubMethod(w, r, http.MethodPut) || !hubDecode(w, r, &input, "name") {
		return
	}
	if _, err := p.store.SetOrganizationName(input.Name); err != nil {
		hubStoreError(w, err)
		return
	}
	hubJSON(w, http.StatusOK, p.organizationResponse())
}

func (p *portalHub) manageMembers(w http.ResponseWriter, r *http.Request, member hubstate.Member) {
	if !requireActiveMember(w, member) || !requireHubAdmin(w, member) || !hubMethod(w, r, http.MethodGet) {
		return
	}
	members := make([]hubstate.Member, 0, p.store.ActiveMemberCount()+1+len(p.store.Members()))
	members = append(members, p.localMember())
	members = append(members, p.store.Members()...)
	hubJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (p *portalHub) updateMember(h *Handler, w http.ResponseWriter, r *http.Request, actor hubstate.Member) {
	if !requireActiveMember(w, actor) || !requireHubAdmin(w, actor) {
		return
	}
	rawID := strings.TrimPrefix(r.URL.Path, "/api/hub/members/")
	id, err := url.PathUnescape(rawID)
	if err != nil || id == "" || strings.Contains(id, "/") {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Member ID is invalid")
		return
	}
	if id == hubstate.LocalMemberID {
		hubHTTPError(w, http.StatusForbidden, "permission_denied", "The local owner cannot be changed")
		return
	}
	var input struct {
		Role             string   `json:"role"`
		Status           string   `json:"status"`
		ClusterIDs       []string `json:"clusterIds"`
		KubernetesGroups []string `json:"kubernetesGroups"`
	}
	if !hubMethod(w, r, http.MethodPut) ||
		!hubDecode(w, r, &input, "role", "status", "clusterIds", "kubernetesGroups") {
		return
	}
	member, changed, err := p.store.UpdateMember(id, hubstate.MemberInput{
		Role: input.Role, Status: input.Status, ClusterIDs: input.ClusterIDs,
		KubernetesGroups: input.KubernetesGroups,
	})
	if err != nil {
		hubStoreError(w, err)
		return
	}
	if changed {
		h.sessions.cancelOwner(id)
	}
	hubJSON(w, http.StatusOK, member)
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
