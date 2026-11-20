package hubstate

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestClusterDeletePersistsWithCredentialHistory(t *testing.T) {
	store, _ := testStore(t)
	const organizationID = "self-hosted"
	const localOwnerID = "portal:recovery"
	if err := store.EnsurePlatformOwner(localOwnerID, "kubeatlas:recovery", "Recovery"); err != nil {
		t.Fatal(err)
	}
	alpha := cluster(t, store, "alpha")
	beta := cluster(t, store, "beta")
	if _, err := store.SaveGroup(GroupInput{ID: "pair", Name: "Cluster pair", ClusterIDs: []string{"alpha", "beta"}}); err != nil {
		t.Fatal(err)
	}
	memberID := "oidc:" + strings.Repeat("a", 64)
	if _, _, err := store.UpsertOIDCMember(memberID, "Legacy member"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateMember(memberID, MemberInput{
		Role: "viewer", Status: "active", ClusterIDs: []string{"alpha", "beta"},
	}); err != nil {
		t.Fatal(err)
	}
	grant, err := store.Issue(localOwnerID, IssueInput{ClusterIDs: []string{"alpha", "beta"}})
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := store.Refresh(grant.Token, localOwnerID, 0)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := store.Issue(localOwnerID, IssueInput{ClusterIDs: []string{"beta"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeAgentToken("alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteRevokedCluster(organizationID, localOwnerID, localOwnerID, "alpha"); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = store.now
	if _, exists := reopened.Cluster("alpha"); exists {
		t.Fatal("deleted cluster survived reopen")
	}
	if group, ok := reopened.Group("pair"); !ok || !slices.Equal(group.ClusterIDs, []string{"beta"}) {
		t.Fatalf("group scope after deletion: %#v", group)
	}
	if member, ok := reopened.MemberForOrganization(organizationID, memberID); !ok || !slices.Equal(member.ClusterIDs, []string{"beta"}) {
		t.Fatalf("legacy member scope after deletion: %#v", member)
	}
	for _, token := range []string{grant.Token, refreshed.Token} {
		history, err := reopened.CredentialScope(token)
		if err != nil || history.RevokedAt == nil || !slices.Equal(history.ClusterIDs, []string{"alpha", "beta"}) {
			t.Fatalf("revoked credential history after deletion: %#v, %v", history, err)
		}
		_, err = reopened.Authenticate(token, localOwnerID)
		errorCode(t, err, "credential_revoked")
	}
	if _, err := reopened.Authenticate(unrelated.Token, localOwnerID); err != nil {
		t.Fatalf("unrelated grant was invalidated: %v", err)
	}
	if _, err := reopened.AuthenticateAgent(beta.Token, "beta"); err != nil {
		t.Fatalf("unrelated agent was invalidated: %v", err)
	}
	newAlpha := cluster(t, reopened, "alpha")
	_, err = reopened.AuthenticateAgent(alpha.Token, "alpha")
	errorCode(t, err, "credential_invalid")
	if _, err := reopened.AuthenticateAgent(newAlpha.Token, "alpha"); err != nil {
		t.Fatalf("new Agent credential was invalidated: %v", err)
	}
	_, err = reopened.Refresh(refreshed.Token, localOwnerID, 0)
	errorCode(t, err, "credential_revoked")
}

func TestClusterDeletePersistenceFailureLeavesReferencesIntact(t *testing.T) {
	store, _ := testStore(t)
	const localOwnerID = "portal:recovery"
	if err := store.EnsurePlatformOwner(localOwnerID, "kubeatlas:recovery", "Recovery"); err != nil {
		t.Fatal(err)
	}
	cluster(t, store, "alpha")
	grant := issue(t, store, 0)
	if _, err := store.SaveGroup(GroupInput{ID: "only", Name: "Single cluster", ClusterIDs: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeAgentToken("alpha"); err != nil {
		t.Fatal(err)
	}
	path := store.path
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(filepath.Dir(path), "blocked")
	if err := os.Mkdir(store.path, 0o700); err != nil {
		t.Fatal(err)
	}
	revoked, err := store.DeleteRevokedCluster("self-hosted", localOwnerID, localOwnerID, "alpha")
	errorCode(t, err, "store_unavailable")
	if len(revoked) != 0 {
		t.Fatal("failed deletion reported revoked credentials")
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed delete changed persisted state: %v", err)
	}
	if cluster, ok := store.Cluster("alpha"); !ok || cluster.RevokedAt == nil {
		t.Fatal("failed delete changed cluster in memory")
	}
	if group, ok := store.Group("only"); !ok || !slices.Equal(group.ClusterIDs, []string{"alpha"}) {
		t.Fatal("failed delete changed group in memory")
	}
	if _, err := store.Authenticate(grant.Token, "owner"); err != nil {
		t.Fatalf("failed delete invalidated an existing grant: %v", err)
	}
}
