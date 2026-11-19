package hubstate

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestClusterTransferPreservesAgentCredentialAndRevocationAcrossReopen(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		name := "active"
		if revoked {
			name = "revoked"
		}
		t.Run(name, func(t *testing.T) {
			store, now := testStore(t)
			const recoveryID = "portal:transfer"
			if err := store.EnsurePlatformOwner(recoveryID, "kubeatlas:transfer", "Recovery"); err != nil {
				t.Fatal(err)
			}
			destination, err := store.CreateOrganization("Destination", recoveryID)
			if err != nil {
				t.Fatal(err)
			}
			agent := cluster(t, store, "moving")
			if revoked {
				*now = now.Add(time.Second)
				if err := store.RevokeAgentToken("moving"); err != nil {
					t.Fatal(err)
				}
			}
			before := store.data.Clusters[agent.Cluster.ID]
			*now = now.Add(time.Second)
			transferred, err := store.TransferCluster(agent.Cluster.ID, destination.ID)
			if err != nil {
				t.Fatal(err)
			}
			if transferred.OrganizationID != destination.ID || !transferred.UpdatedAt.Equal(*now) || !transferred.CreatedAt.Equal(before.CreatedAt) {
				t.Fatal("transfer changed identity timestamps incorrectly or failed to move ownership")
			}
			reopened, err := OpenStore(store.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []*Store{store, reopened} {
				persisted := candidate.data.Clusters[agent.Cluster.ID]
				if persisted.Digest != before.Digest || (persisted.RevokedAt != nil) != revoked {
					t.Fatal("transfer changed the Agent credential digest or revocation state")
				}
				if revoked && !persisted.RevokedAt.Equal(*before.RevokedAt) {
					t.Fatal("transfer replaced the original revocation timestamp")
				}
				current, err := candidate.AuthenticateAgent(agent.Token, agent.Cluster.ID)
				if revoked {
					errorCode(t, err, "credential_invalid")
					if _, err := candidate.RotateAgentTokenForOrganization(destination.ID, agent.Cluster.ID); err == nil {
						t.Fatal("ownership transfer allowed rotation to revive a revoked Agent")
					}
				} else if err != nil || current.OrganizationID != destination.ID {
					t.Fatalf("original active Agent token did not authenticate in the destination: %v", err)
				}
				if _, exists := candidate.ClusterForOrganization("self-hosted", agent.Cluster.ID); exists {
					t.Fatal("source organization retained the transferred cluster")
				}
			}
		})
	}
}

func TestClusterTransferRemovesSourceGrantsWithoutExpandingDestinationScope(t *testing.T) {
	f := newOrganizationBoundaryFixture(t, "")
	store := f.store
	if _, err := store.CreateClusterForOrganization(f.organizationA, ClusterInput{ID: "unrelated-source", Name: "Unrelated source"}); err != nil {
		t.Fatal(err)
	}
	unrelatedID := "oidc:" + strings.Repeat("d", 64)
	addOrganizationBoundaryAccount(t, store, unrelatedID, "active", OrganizationMembership{OrganizationID: f.organizationA, Role: "member"})
	unrelatedCredential, err := store.IssueForMemberInOrganization(f.organizationA, unrelatedID, IssueInput{ClusterIDs: []string{"unrelated-source"}, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []struct {
		organizationID, id string
		clusterIDs         []string
	}{
		{f.organizationA, "source-group", []string{"boundary-a", "unrelated-source"}},
		{f.organizationB, "target-group", []string{"boundary-b"}},
	} {
		if _, err := store.SaveGroupForOrganization(group.organizationID, GroupInput{ID: group.id, Name: group.id, ClusterIDs: group.clusterIDs}); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	next := store.next(store.now().UTC())
	for _, input := range []struct{ organizationID, accountID, clusterID string }{
		{f.organizationA, f.ownerID, "boundary-a"},
		{f.organizationB, f.bystanderID, "boundary-b"},
	} {
		membership := next.Memberships[input.organizationID][input.accountID]
		membership.LegacyRestricted, membership.ClusterIDs = true, []string{input.clusterID}
		next.Memberships[input.organizationID][input.accountID] = membership
	}
	err = store.commit(next)
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransferCluster("boundary-a", f.organizationB); err != nil {
		t.Fatal(err)
	}
	member, exists := store.MemberForOrganization(f.organizationA, f.ownerID)
	if !exists || !member.LegacyRestricted || len(member.ClusterIDs) != 0 {
		t.Fatal("transfer retained the source member's explicit cluster scope")
	}
	if groups := store.GroupsForOrganization(f.organizationA); len(groups) != 1 || !slices.Equal(groups[0].ClusterIDs, []string{"unrelated-source"}) {
		t.Fatal("transfer did not remove only the moved cluster from its source group")
	}
	if groups := store.GroupsForOrganization(f.organizationB); len(groups) != 1 || !slices.Equal(groups[0].ClusterIDs, []string{"boundary-b"}) {
		t.Fatal("transfer expanded the destination group")
	}
	_, err = store.AuthenticateForOrganization(f.credentialA.Token, f.ownerID, f.organizationA)
	errorCode(t, err, "credential_revoked")
	for _, input := range []struct{ token, accountID, organizationID string }{
		{f.credentialB.Token, f.ownerID, f.organizationB},
		{unrelatedCredential.Token, unrelatedID, f.organizationA},
	} {
		if _, err := store.AuthenticateForOrganization(input.token, input.accountID, input.organizationID); err != nil {
			t.Fatalf("transfer revoked an unrelated credential: %v", err)
		}
	}
	if listed := store.CredentialsForOrganization(f.organizationB, f.ownerID); len(listed) != 1 || !slices.Equal(listed[0].ClusterIDs, []string{"boundary-b"}) {
		t.Fatal("transfer expanded an existing destination MCP credential")
	}
	_, err = store.IssueForMemberInOrganization(f.organizationB, f.bystanderID, IssueInput{ClusterIDs: []string{"boundary-a"}, ReadOnly: true})
	errorCode(t, err, "permission_denied")
	if _, err := OpenStore(store.path); err != nil {
		t.Fatalf("transferred state with retained revoked credential metadata did not reopen: %v", err)
	}
}

func TestClusterTransferValidationAndPersistenceFailuresAreAtomic(t *testing.T) {
	f := newOrganizationBoundaryFixture(t, "")
	store := f.store
	disk, memory := snapshotOrganizationBoundary(t, store)
	for _, input := range []struct{ clusterID, organizationID, code string }{
		{"missing", f.organizationB, "cluster_not_found"},
		{"boundary-a", "missing", "organization_not_found"},
		{"boundary-a", f.organizationA, "conflict"},
	} {
		_, err := store.TransferCluster(input.clusterID, input.organizationID)
		errorCode(t, err, input.code)
		requireOrganizationBoundaryUnchanged(t, store, disk, memory)
	}
	originalPath := store.path
	blocked := filepath.Join(filepath.Dir(originalPath), "blocked-transfer")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	store.path = blocked
	defer func() { store.path = originalPath }()
	_, err := store.TransferCluster("boundary-a", f.organizationB)
	errorCode(t, err, "store_unavailable")
	store.path = originalPath
	requireOrganizationBoundaryUnchanged(t, store, disk, memory)
}
