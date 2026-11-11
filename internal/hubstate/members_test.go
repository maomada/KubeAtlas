package hubstate

import (
	"bytes"
	"encoding/json"
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
	if err != nil || !created || member.Status != "pending" || member.Role != "member" || len(member.ClusterIDs) != 0 {
		t.Fatalf("new member = %#v, created=%v, err=%v", member, created, err)
	}
	if _, err := store.IssueForMember(id, IssueInput{Label: "pending", ClusterIDs: []string{"alpha"}}); err == nil {
		t.Fatal("pending member received a credential")
	} else {
		errorCode(t, err, "member_pending")
	}
	member, changed, err := store.UpdateMember(id, MemberInput{
		Role: "member", Status: "active", ClusterIDs: []string{"alpha"},
		KubernetesGroups: []string{"radar:reader"},
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
		KubernetesGroups: []string{"radar:reader"},
	}); err != nil {
		t.Fatal(err)
	}
	issued, err = store.IssueForMember(id, IssueInput{Label: "viewer", ClusterIDs: []string{"alpha"}, ReadOnly: false})
	if err != nil || !issued.Credential.ReadOnly {
		t.Fatalf("viewer credential = %#v, err=%v", issued.Credential, err)
	}
	if _, _, err := store.UpdateMember(id, MemberInput{
		Role: "viewer", Status: "active", ClusterIDs: []string{},
		KubernetesGroups: []string{"radar:reader"},
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
	delete(legacy, "organization")
	delete(legacy, "members")
	delete(legacy, "connectionSettings")
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
	if organization := migrated.Organization(); organization.ID != "self-hosted" || organization.Name != "KubeAtlas" ||
		len(migrated.Members()) != 0 || len(migrated.Clusters()) != 1 {
		t.Fatalf("legacy state defaults were not applied: organization=%#v, members=%d, clusters=%d",
			organization, len(migrated.Members()), len(migrated.Clusters()))
	}
	if group, ok := migrated.Group("team"); !ok || !slices.Equal(group.ClusterIDs, []string{"alpha"}) {
		t.Fatalf("legacy group was lost: %#v, ok=%v", group, ok)
	}
	if _, err := migrated.Authenticate(issued.Token, "owner"); err != nil {
		t.Fatalf("legacy credential was lost: %v", err)
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
	if persisted["organization"] == nil || persisted["members"] == nil {
		t.Fatal("legacy store extension was not persisted on mutation")
	}
	reopened, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = store.now
	if organization := reopened.Organization(); organization.Name != "KubeAtlas Local" ||
		len(reopened.Clusters()) != 1 || len(reopened.Members()) != 0 {
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
	member, ok := store.Member(id)
	if !ok || member.Role != "member" || member.Status != "active" || !slices.Equal(member.ClusterIDs, []string{"alpha"}) {
		t.Fatalf("failed member update changed memory state: %#v", member)
	}
	if _, err := store.Authenticate(issued.Token, id); err != nil {
		t.Fatalf("failed member update revoked its credential: %v", err)
	}
}
