package hubstate

import (
	"bytes"
	"encoding/json"
	"github.com/maomada/KubeAtlas/pkg/compat"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMemberPersistenceScopeAndCredentialRevocation(t *testing.T) {
	store, _ := testStore(t)
	cluster(t, store, "alpha")
	cluster(t, store, "beta")
	id := "oidc:" + strings.Repeat("a", 64)
	member, created, err := store.UpsertOIDCMember(id, "Alice")
	if err != nil || !created || member.Status != "pending" || member.Role != "" || len(member.ClusterIDs) != 0 {
		t.Fatalf("new member = %#v, created=%v, err=%v", member, created, err)
	}
	if member.AuthorizationRevision != 1 {
		t.Fatalf("new account authorization revision = %d, want 1", member.AuthorizationRevision)
	}
	if _, exists := store.MemberForOrganization("self-hosted", id); exists {
		t.Fatal("new OIDC account received a membership before organization approval")
	}
	if _, err := store.IssueForMember(id, IssueInput{Label: "pending", ClusterIDs: []string{"alpha"}}); err == nil {
		t.Fatal("pending member received a credential")
	} else {
		errorCode(t, err, "member_pending")
	}
	member, changed, err := store.UpdateMember(id, MemberInput{
		Role: "member", Status: "active", ClusterIDs: []string{"alpha"},
		KubernetesGroups: []string{"kubeatlas:idp:reader"},
	})
	if err != nil || !changed || member.Status != "active" {
		t.Fatalf("activate member = %#v, changed=%v, err=%v", member, changed, err)
	}
	issued, err := store.IssueForMember(id, IssueInput{Label: "scoped", ClusterIDs: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := store.CredentialOwner(issued.Token); err != nil || owner != id {
		t.Fatalf("verified owner = %q, err=%v", owner, err)
	}
	if _, err := store.Authenticate(issued.Token, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.IssueForMember(id, IssueInput{Label: "out-of-scope", ClusterIDs: []string{"beta"}}); err == nil {
		t.Fatal("member received a credential outside its current scope")
	} else {
		errorCode(t, err, "permission_denied")
	}
	if _, _, err := store.UpdateMember(id, MemberInput{
		Role: "viewer", Status: "active", ClusterIDs: []string{"alpha"},
		KubernetesGroups: []string{"kubeatlas:idp:reader"},
	}); err != nil {
		t.Fatal(err)
	}
	issued, err = store.IssueForMember(id, IssueInput{Label: "viewer", ClusterIDs: []string{"alpha"}, ReadOnly: false})
	if err != nil || !issued.Credential.ReadOnly {
		t.Fatalf("viewer credential = %#v, err=%v", issued.Credential, err)
	}
	if _, _, err := store.UpdateMember(id, MemberInput{
		Role: "viewer", Status: "active", ClusterIDs: []string{},
		KubernetesGroups: []string{"kubeatlas:idp:reader"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(issued.Token, id); err == nil {
		t.Fatal("credential survived an authorization change")
	} else {
		errorCode(t, err, "credential_revoked")
	}
	reopened, err := OpenStore(filepath.Join(filepath.Dir(store.path), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	persisted, ok := reopened.Member(id)
	if !ok || persisted.Status != "active" || persisted.Role != "viewer" || len(persisted.ClusterIDs) != 0 {
		t.Fatalf("persisted member = %#v, ok=%v", persisted, ok)
	}
	if organization := reopened.Organization(); organization.ID != "self-hosted" || organization.Name != "KubeAtlas" {
		t.Fatalf("persisted organization = %#v", organization)
	}
}

func TestMemberValidationRejectsUnsafeGroupsAndOwnerRole(t *testing.T) {
	store, _ := testStore(t)
	cluster(t, store, "alpha")
	id := "oidc:" + strings.Repeat("b", 64)
	if _, _, err := store.UpsertOIDCMember(id, "Bob"); err != nil {
		t.Fatal(err)
	}
	for _, input := range []MemberInput{
		{Role: "owner", Status: "active"},
		{Role: "member", Status: "active", ClusterIDs: []string{"missing"}},
		{Role: "member", Status: "active", KubernetesGroups: []string{"system:masters"}},
		{Role: "member", Status: "active", KubernetesGroups: []string{"arbitrary"}},
	} {
		if _, _, err := store.UpdateMember(id, input); err == nil {
			t.Fatalf("unsafe member update accepted: %#v", input)
		}
	}
}

func TestDisabledMemberCannotIssueCredentials(t *testing.T) {
	store, _ := testStore(t)
	cluster(t, store, "alpha")
	id := "oidc:" + strings.Repeat("c", 64)
	if _, _, err := store.UpsertOIDCMember(id, "Casey"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateMember(id, MemberInput{
		Role: "member", Status: "active", ClusterIDs: []string{"alpha"},
		KubernetesGroups: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	issued, err := store.IssueForMember(id, IssueInput{Label: "active", ClusterIDs: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateMember(id, MemberInput{
		Role: "member", Status: "disabled", ClusterIDs: []string{"alpha"},
		KubernetesGroups: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(issued.Token, id); err == nil {
		t.Fatal("disabled member credential remained active")
	} else {
		errorCode(t, err, "credential_revoked")
	}
	if _, err := store.IssueForMember(id, IssueInput{Label: "disabled", ClusterIDs: []string{"alpha"}}); err == nil {
		t.Fatal("disabled member received a credential")
	} else {
		errorCode(t, err, "member_disabled")
	}
}

func TestLegacyStoreMigrationPreservesExistingState(t *testing.T) {
	store, _ := testStore(t)
	cluster(t, store, "alpha")
	cluster(t, store, "beta")
	if _, err := store.SaveGroup(GroupInput{ID: "team", Name: "Team", ClusterIDs: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	issued, err := store.Issue("owner", IssueInput{Label: "automation", ClusterIDs: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["version"] = json.RawMessage("1")
	for _, section := range []string{"organizations", "accounts", "memberships"} {
		delete(legacy, section)
	}
	for _, section := range []string{"clusters", "groups", "credentials"} {
		var records map[string]map[string]json.RawMessage
		if err := json.Unmarshal(legacy[section], &records); err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			delete(record, "organizationId")
		}
		legacy[section], err = json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
	}
	legacy["organization"], _ = json.Marshal(Organization{ID: "self-hosted", Name: "KubeAtlas"})
	now := store.now().UTC()
	adminID, memberID, viewerID := "oidc:"+strings.Repeat("a", 64), "oidc:"+strings.Repeat("b", 64), "oidc:"+strings.Repeat("c", 64)
	legacyMembers := map[string]Member{
		adminID: {ID: adminID, UserID: "kubeatlas:" + adminID, DisplayName: "Legacy Admin", Role: "admin", Status: "active",
			ClusterIDs: []string{}, KubernetesGroups: []string{compat.LegacyTierPrefix + "legacy-admin"}, CreatedAt: now, UpdatedAt: now},
		memberID: {ID: memberID, UserID: "kubeatlas:" + memberID, DisplayName: "Legacy Member", Role: "member", Status: "active",
			ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{compat.LegacyTierPrefix + "legacy-member"}, CreatedAt: now, UpdatedAt: now},
		viewerID: {ID: viewerID, UserID: "kubeatlas:" + viewerID, DisplayName: "Legacy Viewer", Role: "viewer", Status: "active",
			ClusterIDs: []string{}, KubernetesGroups: []string{compat.LegacyTierPrefix + "legacy-viewer"}, CreatedAt: now, UpdatedAt: now},
	}
	legacy["members"], err = json.Marshal(legacyMembers)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	migrated, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	migrated.now = store.now
	for _, id := range []string{adminID, memberID, viewerID} {
		if got := accountRevision(t, migrated, id); got != 0 {
			t.Fatalf("migrated legacy account %s revision = %d, want 0", id, got)
		}
	}
	if organization := migrated.Organization(); organization.ID != "self-hosted" || organization.Name != "KubeAtlas" ||
		len(migrated.Members()) != 3 || len(migrated.Clusters()) != 2 {
		t.Fatalf("legacy state defaults were not applied: organization=%#v, members=%d, clusters=%d",
			organization, len(migrated.Members()), len(migrated.Clusters()))
	}
	if group, ok := migrated.Group("team"); !ok || !slices.Equal(group.ClusterIDs, []string{"alpha"}) {
		t.Fatalf("legacy group was lost: %#v, ok=%v", group, ok)
	}
	if _, err := migrated.Authenticate(issued.Token, "owner"); err != nil {
		t.Fatalf("legacy credential was lost: %v", err)
	}
	if store.data.Credentials[issued.Credential.ID].Digest != migrated.data.Credentials[issued.Credential.ID].Digest {
		t.Fatal("legacy credential hash changed during migration")
	}
	for _, test := range []struct {
		id  string
		ids []string
	}{
		{adminID, []string{"alpha", "beta"}},
		{memberID, []string{"alpha"}},
		{viewerID, []string{}},
	} {
		ids, err := migrated.MemberClusterIDsForOrganization("self-hosted", test.id)
		if err != nil || !slices.Equal(ids, test.ids) {
			t.Fatalf("legacy scope for %s = %v, err=%v; want %v", test.id, ids, err, test.ids)
		}
		if migrated.RoleGroupForMember("self-hosted", test.id) != "" {
			t.Fatalf("legacy member %s inherited a new role group", test.id)
		}
		member, ok := migrated.MemberForOrganization("self-hosted", test.id)
		if !ok || !member.LegacyRestricted {
			t.Fatalf("legacy restriction marker missing for %s: %#v", test.id, member)
		}
		if !slices.Equal(member.KubernetesGroups, legacyMembers[test.id].KubernetesGroups) {
			t.Fatalf("legacy Kubernetes groups changed for %s: %v", test.id, member.KubernetesGroups)
		}
	}
	if err := migrated.EnsurePlatformOwner("portal:recovery", "kubeatlas:recovery", "Recovery"); err != nil {
		t.Fatal(err)
	}
	addOrganizationBoundaryAccount(t, migrated, "oidc:"+strings.Repeat("e", 64), "active", OrganizationMembership{OrganizationID: "self-hosted", Role: "owner"})
	if _, _, err := migrated.UpdateOrganizationMemberRole("self-hosted", adminID, "member"); err != nil {
		t.Fatal(err)
	}
	admin, _ := migrated.MemberForOrganization("self-hosted", adminID)
	adminScope, err := migrated.MemberClusterIDsForOrganization("self-hosted", adminID)
	if err != nil || len(adminScope) != 0 || len(admin.KubernetesGroups) != 0 || admin.LegacyAllClusters {
		t.Fatalf("demoted legacy owner retained elevated grants: %#v scope=%v err=%v", admin, adminScope, err)
	}
	if _, err := migrated.SetOrganizationName("KubeAtlas Local"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]json.RawMessage
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["organizations"] == nil || persisted["accounts"] == nil || persisted["memberships"] == nil {
		t.Fatal("canonical v2 account and membership state was not persisted on mutation")
	}
	reopened, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = store.now
	if organization := reopened.Organization(); organization.Name != "KubeAtlas Local" ||
		len(reopened.Clusters()) != 2 || len(reopened.Members()) != 5 {
		t.Fatalf("migrated state did not survive restart: organization=%#v", organization)
	}
	if _, err := reopened.Authenticate(issued.Token, "owner"); err != nil {
		t.Fatalf("migrated credential was lost: %v", err)
	}
}

func TestMemberUpdatePersistenceFailureIsAtomic(t *testing.T) {
	store, _ := testStore(t)
	cluster(t, store, "alpha")
	id := "oidc:" + strings.Repeat("e", 64)
	if _, _, err := store.UpsertOIDCMember(id, "Emery"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateMember(id, MemberInput{
		Role: "member", Status: "active", ClusterIDs: []string{"alpha"},
		KubernetesGroups: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	issued, err := store.IssueForMember(id, IssueInput{Label: "active", ClusterIDs: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	beforeRevision := accountRevision(t, store, id)
	blocker := filepath.Join(filepath.Dir(store.path), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalPath := store.path
	store.path = filepath.Join(blocker, "state.json")
	_, _, updateErr := store.UpdateMember(id, MemberInput{
		Role: "viewer", Status: "disabled", ClusterIDs: []string{},
		KubernetesGroups: []string{},
	})
	store.path = originalPath
	if updateErr == nil {
		t.Fatal("member update succeeded when persistence failed")
	}
	errorCode(t, updateErr, "store_unavailable")
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed member update changed persisted state")
	}
	if got := accountRevision(t, store, id); got != beforeRevision {
		t.Fatalf("failed member update changed authorization revision to %d", got)
	}
	member, ok := store.Member(id)
	if !ok || member.Role != "member" || member.Status != "active" || !slices.Equal(member.ClusterIDs, []string{"alpha"}) {
		t.Fatalf("failed member update changed memory state: %#v", member)
	}
	if _, err := store.Authenticate(issued.Token, id); err != nil {
		t.Fatalf("failed member update revoked its credential: %v", err)
	}
}
