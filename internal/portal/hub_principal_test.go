package portal

import (
	"slices"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func TestPrincipalGroupsKeepRecoveryIdentityExactAndSortOrganizationRoles(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	p := h.liveHub
	recoveryGroups := p.principalGroups(p.ownerID, "self-hosted", p.localMember())
	if !slices.Equal(recoveryGroups, []string{"radar:idp:operators"}) {
		t.Fatalf("recovery principal groups = %v", recoveryGroups)
	}
	ownerID := "oidc:" + strings.Repeat("9", 64)
	if _, _, err := p.store.UpsertOIDCMember(ownerID, "Organization owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.UpdatePlatformUser(ownerID, "active", []hubstate.OrganizationMembership{{OrganizationID: "self-hosted", Role: "owner"}}); err != nil {
		t.Fatal(err)
	}
	member, ok := p.store.MemberForOrganization("self-hosted", ownerID)
	if !ok {
		t.Fatal("organization owner membership was not stored")
	}
	member.KubernetesGroups = []string{"z-custom", "a-custom", "a-custom"}
	groups := p.principalGroups(ownerID, "self-hosted", member)
	want := []string{"a-custom", "kubeatlas:hub:owner", "z-custom"}
	if !slices.Equal(groups, want) || !slices.IsSorted(groups) {
		t.Fatalf("organization principal groups = %v, want sorted %v", groups, want)
	}
}

func TestRecoveryHubResponseUsesOrganizationOwnerRole(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	cookie := loginForTest(t, h)
	response := hubRequest(t, h, cookie, "", "GET", "/api/hub", "", 200)
	shape := decodeHubResponse[struct {
		PlatformOwner bool `json:"platformOwner"`
		Member        struct {
			ID     string `json:"id"`
			Role   string `json:"role"`
			Status string `json:"status"`
		} `json:"member"`
	}](t, response)
	if !shape.PlatformOwner || shape.Member.ID != hubstate.LocalMemberID || shape.Member.Role != "owner" || shape.Member.Status != "active" {
		t.Fatalf("public recovery shape = %#v", shape)
	}
	organizations := decodeHubResponse[struct {
		Organizations []struct {
			Role string `json:"role"`
		} `json:"organizations"`
	}](t, hubRequest(t, h, cookie, "", "GET", "/api/hub/organizations", "", 200))
	if len(organizations.Organizations) == 0 {
		t.Fatal("recovery account has no public organizations")
	}
	for _, organization := range organizations.Organizations {
		if organization.Role != "owner" {
			t.Fatalf("public recovery organization role = %q", organization.Role)
		}
	}
	created := decodeHubResponse[struct {
		Role string `json:"role"`
	}](t, hubRequest(t, h, cookie, "", "POST", "/api/hub/organizations", `{"name":"Public role test","ownerMemberId":"local"}`, 201))
	if created.Role != "owner" {
		t.Fatalf("created organization role = %q", created.Role)
	}
}
