package portal

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func TestHubMemberStatusAndUnresolvedRoleResponse(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	for _, test := range []struct {
		id     string
		status string
	}{
		{id: "oidc:" + strings.Repeat("a", 64), status: "pending"},
		{id: "oidc:" + strings.Repeat("b", 64), status: "active"},
	} {
		if _, _, err := h.liveHub.store.UpsertOIDCMember(test.id, test.status); err != nil {
			t.Fatal(err)
		}
		if test.status != "pending" {
			if _, err := h.liveHub.store.UpdatePlatformUser(test.id, test.status, nil); err != nil {
				t.Fatal(err)
			}
		}
		response := hubRequest(t, h, hubTestMemberSession(t, h, test.id), "", http.MethodGet, "/api/hub", "", http.StatusOK)
		var body map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		var member map[string]json.RawMessage
		if err := json.Unmarshal(body["member"], &member); err != nil {
			t.Fatalf("member response decode: %v", err)
		}
		var status string
		if err := json.Unmarshal(member["status"], &status); err != nil || status != test.status {
			t.Fatalf("member status = %q, want %q (error %v)", status, test.status, err)
		}
		if _, exists := member["role"]; exists {
			t.Fatalf("unresolved organization exposed a role: %s", response.Body.String())
		}
		if active, exists := body["activeOrganizationId"]; !exists || string(active) != "null" {
			t.Fatalf("activeOrganizationId = %s, exists=%v; want null", active, exists)
		}
		var publicMember hubstate.Member
		if err := json.Unmarshal(body["member"], &publicMember); err != nil {
			t.Fatal(err)
		}
		account, ok := h.liveHub.store.Account(test.id)
		if !ok || publicMember.ClusterIDs == nil || publicMember.KubernetesGroups == nil ||
			len(publicMember.ClusterIDs) != 0 || len(publicMember.KubernetesGroups) != 0 ||
			publicMember.CreatedAt.IsZero() || publicMember.UpdatedAt.IsZero() ||
			!publicMember.CreatedAt.Equal(account.CreatedAt) || !publicMember.UpdatedAt.Equal(account.UpdatedAt) {
			t.Fatalf("unresolved member shape = %#v; account=%#v", publicMember, account)
		}
	}

	localCookie := loginForTest(t, h)
	wantStatuses := map[string]string{}
	for index, status := range []string{"pending", "active", "disabled"} {
		id := "oidc:" + strings.Repeat(string(rune('c'+index)), 64)
		if _, _, err := h.liveHub.store.UpsertOIDCMember(id, status); err != nil {
			t.Fatal(err)
		}
		if _, err := h.liveHub.store.UpdatePlatformUser(id, status, []hubstate.OrganizationMembership{{OrganizationID: "self-hosted", Role: "member"}}); err != nil {
			t.Fatal(err)
		}
		wantStatuses[id] = status
	}
	membersResponse := hubRequest(t, h, localCookie, "", http.MethodGet, "/api/hub/members", "", http.StatusOK)
	var membersEnvelope struct {
		Members []hubstate.Member `json:"members"`
	}
	if err := json.Unmarshal(membersResponse.Body.Bytes(), &membersEnvelope); err != nil {
		t.Fatal(err)
	}
	for _, member := range membersEnvelope.Members {
		if status, ok := wantStatuses[member.ID]; ok {
			if member.Status != status || member.Role != "member" {
				t.Fatalf("public member status/role = %#v, want status %q and role member", member, status)
			}
			delete(wantStatuses, member.ID)
		}
	}
	if len(wantStatuses) != 0 {
		t.Fatalf("organization member response omitted statuses: %#v", wantStatuses)
	}
}

func TestHubOrganizationRolesAndAdminResponseEnvelopes(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	localCookie := loginForTest(t, h)
	localMembers := hubRequest(t, h, localCookie, "", http.MethodGet, "/api/hub/members", "", http.StatusOK)
	var localEnvelope struct {
		Members []hubstate.Member `json:"members"`
	}
	if err := json.Unmarshal(localMembers.Body.Bytes(), &localEnvelope); err != nil {
		t.Fatal(err)
	}
	localRecoveryFound := false
	for _, member := range localEnvelope.Members {
		if member.ID == hubstate.LocalMemberID {
			localRecoveryFound = true
			if member.Role != "owner" {
				t.Fatalf("public local recovery role = %q", member.Role)
			}
		}
	}
	if !localRecoveryFound {
		t.Fatalf("organization member list did not expose public local ID: %s", localMembers.Body.String())
	}
	hubRequest(t, h, localCookie, "", http.MethodPut, "/api/hub/members/local", `{"role":"member"}`, http.StatusForbidden)
	hubRequest(t, h, localCookie, "", http.MethodDelete, "/api/hub/members/local", "", http.StatusForbidden)
	if recovery, ok := h.liveHub.store.MemberForOrganization("self-hosted", h.liveHub.ownerID); !ok || recovery.Role != "owner" {
		t.Fatalf("recovery membership changed through organization member API: %#v, found=%v", recovery, ok)
	}
	memberOrganization, err := h.liveHub.store.CreateOrganization("Member organization", h.liveHub.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	viewerOrganization, err := h.liveHub.store.CreateOrganization("Viewer organization", h.liveHub.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	memberID := "oidc:" + strings.Repeat("d", 64)
	if _, _, err := h.liveHub.store.UpsertOIDCMember(memberID, "Contract user"); err != nil {
		t.Fatal(err)
	}
	memberships := []hubstate.OrganizationMembership{
		{OrganizationID: "self-hosted", Role: "owner"},
		{OrganizationID: memberOrganization.ID, Role: "member"},
		{OrganizationID: viewerOrganization.ID, Role: "viewer"},
	}
	if _, err := h.liveHub.store.UpdatePlatformUser(memberID, "active", memberships); err != nil {
		t.Fatal(err)
	}
	organizationResponse := hubRequest(t, h, hubTestMemberSession(t, h, memberID), "", http.MethodGet, "/api/hub/organizations", "", http.StatusOK)
	var organizationEnvelope map[string]json.RawMessage
	if err := json.Unmarshal(organizationResponse.Body.Bytes(), &organizationEnvelope); err != nil {
		t.Fatal(err)
	}
	if len(organizationEnvelope) != 1 {
		t.Fatalf("organization response envelope = %s", organizationResponse.Body.String())
	}
	var organizations []struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(organizationEnvelope["organizations"], &organizations); err != nil {
		t.Fatal(err)
	}
	wantRoles := map[string]string{"self-hosted": "owner", memberOrganization.ID: "member", viewerOrganization.ID: "viewer"}
	if len(organizations) != len(wantRoles) {
		t.Fatalf("organization response = %#v", organizations)
	}
	for _, organization := range organizations {
		if wantRoles[organization.ID] != organization.Role {
			t.Fatalf("public organization role for %q = %q, want one of owner/member/viewer (%#v)", organization.ID, organization.Role, organizations)
		}
		delete(wantRoles, organization.ID)
	}
	if len(wantRoles) != 0 {
		t.Fatalf("organizations missing from public response: %#v", wantRoles)
	}

	usersResponse := hubRequest(t, h, localCookie, "", http.MethodGet, "/api/hub/platform/users", "", http.StatusOK)
	var usersEnvelope map[string]json.RawMessage
	if err := json.Unmarshal(usersResponse.Body.Bytes(), &usersEnvelope); err != nil || len(usersEnvelope) != 1 {
		t.Fatalf("platform users response envelope=%s, error=%v", usersResponse.Body.String(), err)
	}
	var users []map[string]json.RawMessage
	if err := json.Unmarshal(usersEnvelope["users"], &users); err != nil {
		t.Fatal(err)
	}
	localFound := false
	for _, user := range users {
		var id string
		if json.Unmarshal(user["id"], &id) == nil && id == hubstate.LocalMemberID {
			localFound = true
			if _, exists := user["passwordHash"]; exists {
				t.Fatal("platform user response exposed a password hash")
			}
			var platformOwner bool
			if err := json.Unmarshal(user["platformOwner"], &platformOwner); err != nil || !platformOwner {
				t.Fatalf("local platform user = %s", usersResponse.Body.String())
			}
		}
	}
	if !localFound {
		t.Fatalf("raw platform user list omitted the public local account: %s", usersResponse.Body.String())
	}

	updateBody, err := json.Marshal(map[string]any{"status": "active", "memberships": memberships})
	if err != nil {
		t.Fatal(err)
	}
	userUpdate := hubRequest(t, h, localCookie, "", http.MethodPut, "/api/hub/platform/users/"+memberID, string(updateBody), http.StatusOK)
	var rawUser map[string]json.RawMessage
	if err := json.Unmarshal(userUpdate.Body.Bytes(), &rawUser); err != nil {
		t.Fatal(err)
	}
	if _, exists := rawUser["id"]; !exists {
		t.Fatalf("platform user update was not a raw user: %s", userUpdate.Body.String())
	}
	if _, exists := rawUser["user"]; exists {
		t.Fatalf("platform user update unexpectedly wrapped the user: %s", userUpdate.Body.String())
	}

	memberUpdate := hubRequest(t, h, localCookie, "", http.MethodPut, "/api/hub/members/"+memberID, `{"role":"viewer"}`, http.StatusOK)
	var memberEnvelope map[string]json.RawMessage
	if err := json.Unmarshal(memberUpdate.Body.Bytes(), &memberEnvelope); err != nil || len(memberEnvelope) != 1 {
		t.Fatalf("organization member response envelope=%s, error=%v", memberUpdate.Body.String(), err)
	}
	var updatedMember map[string]json.RawMessage
	if err := json.Unmarshal(memberEnvelope["member"], &updatedMember); err != nil {
		t.Fatal(err)
	}
	var role string
	if err := json.Unmarshal(updatedMember["role"], &role); err != nil || role != "viewer" {
		t.Fatalf("wrapped organization member response = %s", memberUpdate.Body.String())
	}
}
