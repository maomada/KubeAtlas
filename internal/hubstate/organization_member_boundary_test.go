package hubstate

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func addOrganizationBoundaryAccount(t *testing.T, store *Store, id, status string, memberships ...OrganizationMembership) {
	t.Helper()
	if _, _, err := store.UpsertOIDCMember(id, "Ordinary organization account"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdatePlatformUser(id, status, memberships); err != nil {
		t.Fatal(err)
	}
}

type organizationBoundaryFixture struct {
	store                            *Store
	recoveryID, ownerID, bystanderID string
	organizationA, organizationB     string
	credentialA, credentialB         IssuedCredential
}

func newOrganizationBoundaryFixture(t *testing.T, secondOwnerStatus string) organizationBoundaryFixture {
	t.Helper()
	store, _ := testStore(t)
	f := organizationBoundaryFixture{store: store, recoveryID: "portal:boundary", ownerID: "oidc:" + strings.Repeat("a", 64), bystanderID: "oidc:" + strings.Repeat("b", 64)}
	if err := store.EnsurePlatformOwner(f.recoveryID, "kubeatlas:boundary", "Platform administrator"); err != nil {
		t.Fatal(err)
	}
	a, err := store.CreateOrganization("Organization A", f.recoveryID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateOrganization("Organization B", f.recoveryID)
	if err != nil {
		t.Fatal(err)
	}
	f.organizationA, f.organizationB = a.ID, b.ID
	addOrganizationBoundaryAccount(t, store, f.ownerID, "active", OrganizationMembership{OrganizationID: a.ID, Role: "owner"}, OrganizationMembership{OrganizationID: b.ID, Role: "member"})
	addOrganizationBoundaryAccount(t, store, f.bystanderID, "active", OrganizationMembership{OrganizationID: b.ID, Role: "owner"})
	if secondOwnerStatus != "" {
		addOrganizationBoundaryAccount(t, store, "oidc:"+strings.Repeat("c", 64), secondOwnerStatus, OrganizationMembership{OrganizationID: a.ID, Role: "owner"})
	}
	for _, input := range []struct{ organizationID, clusterID string }{{a.ID, "boundary-a"}, {b.ID, "boundary-b"}} {
		if _, err := store.CreateClusterForOrganization(input.organizationID, ClusterInput{ID: input.clusterID, Name: input.clusterID}); err != nil {
			t.Fatal(err)
		}
	}
	f.credentialA, err = store.IssueForMemberInOrganization(a.ID, f.ownerID, IssueInput{Label: "A scope", ClusterIDs: []string{"boundary-a"}, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	f.credentialB, err = store.IssueForMemberInOrganization(b.ID, f.ownerID, IssueInput{Label: "B scope", ClusterIDs: []string{"boundary-b"}, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func snapshotOrganizationBoundary(t *testing.T, store *Store) (disk, memory []byte) {
	t.Helper()
	var err error
	disk, err = os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	memory, err = json.Marshal(store.data)
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return disk, memory
}

func requireOrganizationBoundaryUnchanged(t *testing.T, store *Store, disk, memory []byte) {
	t.Helper()
	afterDisk, afterMemory := snapshotOrganizationBoundary(t, store)
	if !bytes.Equal(disk, afterDisk) || !bytes.Equal(memory, afterMemory) {
		t.Fatal("rejected organization mutation changed persisted or in-memory state")
	}
}

func TestOrganizationDirectoryHidesPlatformIdentityWithoutMigratingCompatibilityMembership(t *testing.T) {
	f := newOrganizationBoundaryFixture(t, "")
	if _, _, err := f.store.UpsertOIDCMember(f.ownerID, "admin"); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		organizationID string
		want           []string
	}{{"self-hosted", []string{}}, {f.organizationA, []string{f.ownerID}}, {f.organizationB, []string{f.ownerID, f.bystanderID}}} {
		members := f.store.MembersForOrganization(check.organizationID)
		ids := make([]string, 0, len(members))
		for _, member := range members {
			ids = append(ids, member.ID)
			if member.ID == f.ownerID && member.DisplayName != "admin" {
				t.Fatal("ordinary account named admin was changed or hidden")
			}
		}
		slices.Sort(check.want)
		if !slices.Equal(ids, check.want) {
			t.Fatalf("organization directory IDs=%v, want %v", ids, check.want)
		}
		if recovery, exists := f.store.MemberForOrganization(check.organizationID, f.recoveryID); !exists || recovery.Role != "owner" {
			t.Fatal("listing removed the internal recovery compatibility membership")
		}
	}
	beforeDisk, beforeMemory := snapshotOrganizationBoundary(t, f.store)
	if err := f.store.EnsurePlatformOwner(f.recoveryID, "kubeatlas:boundary", "Platform administrator"); err != nil {
		t.Fatal(err)
	}
	requireOrganizationBoundaryUnchanged(t, f.store, beforeDisk, beforeMemory)
	reopened, err := OpenStore(f.store.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.MembersForOrganization(f.organizationA)) != 1 || !reopened.IsPlatformOwner(f.recoveryID) {
		t.Fatal("restart changed organization filtering or platform recovery authority")
	}
	platformFound := false
	for _, user := range reopened.PlatformUsers() {
		if user.ID == LocalMemberID && user.Local && user.PlatformOwner {
			platformFound = true
		}
	}
	if !platformFound {
		t.Fatal("organization filtering removed the platform account from platform management")
	}
}

func TestOrganizationMemberMutationsRejectSystemAndForeignIDsAtomically(t *testing.T) {
	f := newOrganizationBoundaryFixture(t, "")
	for _, target := range []string{f.recoveryID, f.bystanderID} {
		for _, role := range []string{"owner", "member", "viewer"} {
			beforeDisk, beforeMemory := snapshotOrganizationBoundary(t, f.store)
			member, changed, err := f.store.UpdateOrganizationMemberRole(f.organizationA, target, role)
			errorCode(t, err, "member_not_found")
			if changed || member.ID != "" {
				t.Fatal("rejected update returned a changed or protected member")
			}
			requireOrganizationBoundaryUnchanged(t, f.store, beforeDisk, beforeMemory)
		}
		beforeDisk, beforeMemory := snapshotOrganizationBoundary(t, f.store)
		changed, err := f.store.RemoveOrganizationMember(f.organizationA, target)
		errorCode(t, err, "member_not_found")
		if changed {
			t.Fatal("rejected removal reported a change")
		}
		requireOrganizationBoundaryUnchanged(t, f.store, beforeDisk, beforeMemory)
	}
}

func TestLastOrdinaryOrganizationOwnerRejectsRoleStatusAndMembershipChanges(t *testing.T) {
	for _, secondStatus := range []string{"", "pending", "disabled"} {
		t.Run("second-owner-"+secondStatus, func(t *testing.T) {
			f := newOrganizationBoundaryFixture(t, secondStatus)
			memberships := []OrganizationMembership{{OrganizationID: f.organizationA, Role: "owner"}, {OrganizationID: f.organizationB, Role: "member"}}
			mutations := []struct {
				name string
				run  func() error
			}{
				{"organization-demotion", func() error {
					_, _, err := f.store.UpdateOrganizationMemberRole(f.organizationA, f.ownerID, "viewer")
					return err
				}},
				{"organization-removal", func() error { _, err := f.store.RemoveOrganizationMember(f.organizationA, f.ownerID); return err }},
				{"platform-disable", func() error { _, err := f.store.UpdatePlatformUser(f.ownerID, "disabled", memberships); return err }},
				{"platform-pending", func() error { _, err := f.store.UpdatePlatformUser(f.ownerID, "pending", memberships); return err }},
				{"platform-membership-removal", func() error { _, err := f.store.UpdatePlatformUser(f.ownerID, "active", memberships[1:]); return err }},
				{"platform-role-demotion", func() error {
					_, err := f.store.UpdatePlatformUser(f.ownerID, "active", []OrganizationMembership{{OrganizationID: f.organizationA, Role: "member"}, memberships[1]})
					return err
				}},
			}
			for _, mutation := range mutations {
				t.Run(mutation.name, func(t *testing.T) {
					beforeDisk, beforeMemory := snapshotOrganizationBoundary(t, f.store)
					errorCode(t, mutation.run(), "last_owner")
					requireOrganizationBoundaryUnchanged(t, f.store, beforeDisk, beforeMemory)
					if _, err := f.store.AuthenticateForOrganization(f.credentialA.Token, f.ownerID, f.organizationA); err != nil {
						t.Fatal("failed last-owner change revoked the owner's credential")
					}
				})
			}
		})
	}
}

func TestOrdinaryOwnerChangesWithSecondOwnerPreserveOtherOrganization(t *testing.T) {
	f := newOrganizationBoundaryFixture(t, "active")
	beforeB, _ := f.store.MemberForOrganization(f.organizationB, f.ownerID)
	member, changed, err := f.store.UpdateOrganizationMemberRole(f.organizationA, f.ownerID, "viewer")
	if err != nil || !changed || member.Role != "viewer" {
		t.Fatalf("ordinary owner demotion failed: changed=%v err=%v", changed, err)
	}
	if _, err := f.store.AuthenticateForOrganization(f.credentialA.Token, f.ownerID, f.organizationA); err == nil {
		t.Fatal("demoted organization credential remained valid")
	}
	if removed, err := f.store.RemoveOrganizationMember(f.organizationA, f.ownerID); err != nil || !removed {
		t.Fatalf("ordinary member removal failed: removed=%v err=%v", removed, err)
	}
	afterB, exists := f.store.MemberForOrganization(f.organizationB, f.ownerID)
	// Account-wide revision may advance; organization B's membership grant must not change.
	afterB.AuthorizationRevision = beforeB.AuthorizationRevision
	if !exists || !reflect.DeepEqual(beforeB, afterB) {
		t.Fatal("organization A mutation changed organization B's membership")
	}
	if _, err := f.store.AuthenticateForOrganization(f.credentialB.Token, f.ownerID, f.organizationB); err != nil {
		t.Fatal("organization A mutation revoked organization B's credential")
	}
	reopened, err := OpenStore(f.store.path)
	if err != nil {
		t.Fatal(err)
	}
	if members := reopened.MembersForOrganization(f.organizationA); len(members) != 1 || members[0].ID != "oidc:"+strings.Repeat("c", 64) {
		t.Fatal("restart restored removed or hidden members")
	}
	if recovery, ok := reopened.MemberForOrganization(f.organizationA, f.recoveryID); !ok || recovery.Role != "owner" {
		t.Fatal("ordinary member removal deleted recovery compatibility data")
	}
}
