package hubstate

import (
	"slices"
	"strings"
	"testing"
)

func TestEnrollmentClusterIssuanceRevalidatesApproverMembership(t *testing.T) {
	store, _ := testStore(t)
	const recoveryID = "portal:recovery"
	if err := store.EnsurePlatformOwner(recoveryID, "kubeatlas:recovery", "Recovery"); err != nil {
		t.Fatal(err)
	}
	ownerID := "oidc:" + strings.Repeat("f", 64)
	if _, _, err := store.UpsertOIDCMember(ownerID, "Owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdatePlatformUser(ownerID, "active", []OrganizationMembership{{OrganizationID: "self-hosted", Role: "owner"}}); err != nil {
		t.Fatal(err)
	}
	input := ClusterInput{ID: "enrollment-one", Name: "Enrollment One"}
	if _, err := store.CreateClusterForEnrollment("self-hosted", ownerID, recoveryID, input); err != nil {
		t.Fatalf("active organization owner could not enroll: %v", err)
	}
	if err := store.RevokeAgentTokenForOrganization("self-hosted", input.ID); err != nil {
		t.Fatal(err)
	}
	addOrganizationBoundaryAccount(t, store, "oidc:"+strings.Repeat("e", 64), "active", OrganizationMembership{OrganizationID: "self-hosted", Role: "owner"})
	if _, err := store.RemoveOrganizationMember("self-hosted", ownerID); err != nil {
		t.Fatal(err)
	}
	_, err := store.CreateClusterForEnrollment("self-hosted", ownerID, recoveryID, ClusterInput{ID: "enrollment-two", Name: "Enrollment Two"})
	errorCode(t, err, "permission_denied")
	_, err = store.ReenrollClusterForEnrollment("self-hosted", ownerID, recoveryID, input.ID, input)
	errorCode(t, err, "permission_denied")
	if _, err := store.CreateClusterForEnrollment("self-hosted", recoveryID, recoveryID, ClusterInput{ID: "local-enrollment", Name: "Local Enrollment"}); err != nil {
		t.Fatalf("configured recovery owner could not enroll: %v", err)
	}
}

func TestEnrollmentIssuanceRetainsLegacyClusterRestrictions(t *testing.T) {
	store, _ := testStore(t)
	const recoveryID = "portal:recovery"
	if err := store.EnsurePlatformOwner(recoveryID, "kubeatlas:recovery", "Recovery"); err != nil {
		t.Fatal(err)
	}
	ownerID := "oidc:" + strings.Repeat("a", 64)
	if _, _, err := store.UpsertOIDCMember(ownerID, "Owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdatePlatformUser(ownerID, "active", []OrganizationMembership{{OrganizationID: "self-hosted", Role: "owner"}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"scope-inside", "scope-outside"} {
		if _, err := store.CreateClusterForOrganization("self-hosted", ClusterInput{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
		if err := store.RevokeAgentTokenForOrganization("self-hosted", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := store.UpdateMember(ownerID, MemberInput{
		Role: "viewer", Status: "active", ClusterIDs: []string{"scope-inside"}, KubernetesGroups: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdatePlatformUser(ownerID, "active", []OrganizationMembership{{OrganizationID: "self-hosted", Role: "owner"}}); err != nil {
		t.Fatal(err)
	}
	member, ok := store.MemberForOrganization("self-hosted", ownerID)
	if !ok || !member.LegacyRestricted || member.LegacyAllClusters || !slices.Equal(member.ClusterIDs, []string{"scope-inside"}) {
		t.Fatalf("legacy owner membership = %#v, ok=%v", member, ok)
	}

	_, err := store.CreateClusterForEnrollment("self-hosted", ownerID, recoveryID,
		ClusterInput{ID: "scope-new", Name: "New cluster"})
	errorCode(t, err, "permission_denied")
	_, err = store.ReenrollClusterForEnrollment("self-hosted", ownerID, recoveryID, "scope-outside",
		ClusterInput{ID: "scope-outside", Name: "Outside"})
	errorCode(t, err, "permission_denied")
	issued, err := store.ReenrollClusterForEnrollment("self-hosted", ownerID, recoveryID, "scope-inside",
		ClusterInput{ID: "scope-inside", Name: "Inside"})
	if err != nil {
		t.Fatalf("in-scope re-enrollment failed: %v", err)
	}
	if _, err := store.AuthenticateAgent(issued.Token, "scope-inside"); err != nil {
		t.Fatalf("in-scope fresh token was not accepted: %v", err)
	}
}

func TestTransferClusterRemovesLegacyMemberAndGroupScopes(t *testing.T) {
	store, _ := testStore(t)
	const recoveryID = "portal:recovery"
	if err := store.EnsurePlatformOwner(recoveryID, "kubeatlas:recovery", "Recovery"); err != nil {
		t.Fatal(err)
	}
	organizationA, err := store.CreateOrganization("Scope source", recoveryID)
	if err != nil {
		t.Fatal(err)
	}
	organizationB, err := store.CreateOrganization("Scope destination", recoveryID)
	if err != nil {
		t.Fatal(err)
	}
	cluster, err := store.CreateClusterForOrganization(organizationA.ID, ClusterInput{ID: "transfer-scope", Name: "Transfer scope"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveGroupForOrganization(organizationA.ID, GroupInput{
		ID: "transfer-group", Name: "Transfer group", ClusterIDs: []string{cluster.Cluster.ID},
	}); err != nil {
		t.Fatal(err)
	}
	accountID := "oidc:" + strings.Repeat("e", 64)
	if _, _, err := store.UpsertOIDCMember(accountID, "Scoped member"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdatePlatformUser(accountID, "active", []OrganizationMembership{
		{OrganizationID: organizationA.ID, Role: "member"},
	}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	next := store.next(store.now().UTC())
	membership := next.Memberships[organizationA.ID][accountID]
	membership.LegacyRestricted = true
	membership.ClusterIDs = []string{cluster.Cluster.ID}
	next.Memberships[organizationA.ID][accountID] = membership
	syncAliasesFromPrimary(&next)
	err = store.commit(next)
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransferCluster(cluster.Cluster.ID, organizationB.ID); err != nil {
		t.Fatal(err)
	}
	member, ok := store.MemberForOrganization(organizationA.ID, accountID)
	if !ok || !member.LegacyRestricted || len(member.ClusterIDs) != 0 {
		t.Fatalf("transferred cluster remains in legacy member scope: %#v, ok=%v", member, ok)
	}
	groups := store.GroupsForOrganization(organizationA.ID)
	if len(groups) != 1 || len(groups[0].ClusterIDs) != 0 {
		t.Fatalf("transferred cluster remains in group scope: %#v", groups)
	}
}
