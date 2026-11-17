package hubstate

import (
	"encoding/json"
	"github.com/maomada/KubeAtlas/pkg/compat"
	"slices"
	"strings"
	"testing"
)

func TestConfiguredPlatformOwnerIsOnlyLocalAndOwnsDefaultOrganization(t *testing.T) {
	store, _ := testStore(t)
	if err := store.EnsurePlatformOwner("portal:first", "kubeatlas:first", "First recovery"); err != nil {
		t.Fatal(err)
	}
	account, ok := store.Account("portal:first")
	if !ok || !account.Local || !account.PlatformOwner || account.UserID != "kubeatlas:first" {
		t.Fatalf("configured account = %#v, ok=%v", account, ok)
	}
	membership, ok := store.MemberForOrganization("self-hosted", "portal:first")
	if !ok || membership.Role != "owner" {
		t.Fatalf("default recovery membership = %#v, ok=%v", membership, ok)
	}
	if err := store.EnsurePlatformOwner("portal:second", "kubeatlas:second", "Second recovery"); err != nil {
		t.Fatal(err)
	}
	users := store.PlatformUsers()
	var platformOwners, locals int
	for _, user := range users {
		if user.PlatformOwner {
			platformOwners++
		}
		if user.Local {
			locals++
			if user.ID != LocalMemberID || user.Status != "active" || !user.PlatformOwner {
				t.Fatalf("public recovery identity = %#v", user)
			}
		}
		data, err := json.Marshal(user)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil || fields["local"] == nil || fields["platformOwner"] == nil {
			t.Fatalf("platform flags missing from user JSON: %s err=%v", data, err)
		}
		if user.ID == "portal:first" && (user.Status != "disabled" || len(user.Memberships) != 0) {
			t.Fatalf("superseded recovery account retained access: %#v", user)
		}
	}
	if platformOwners != 1 || locals != 1 {
		t.Fatalf("platform owners=%d local identities=%d users=%#v", platformOwners, locals, users)
	}
	if _, err := store.UpdatePlatformUser("portal:second", "disabled", nil); err == nil {
		t.Fatal("configured recovery identity was disabled")
	} else {
		errorCode(t, err, "immutable_account")
	}
}

func TestEnsurePlatformOwnerMigratesPriorRecoveryMemberships(t *testing.T) {
	store, _ := testStore(t)
	if err := store.EnsurePlatformOwner("portal:old", "kubeatlas:old", "Old recovery"); err != nil {
		t.Fatal(err)
	}
	organization, err := store.CreateOrganization("Secondary", "portal:old")
	if err != nil {
		t.Fatal(err)
	}
	cluster, err := store.CreateClusterForOrganization(organization.ID, ClusterInput{ID: "secondary", Name: "Secondary"})
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	next := store.next(store.now().UTC())
	membership := next.Memberships[organization.ID]["portal:old"]
	membership.LegacyRestricted = true
	membership.ClusterIDs = []string{cluster.Cluster.ID}
	membership.KubernetesGroups = []string{compat.LegacyTierPrefix + "legacy"}
	next.Memberships[organization.ID]["portal:old"] = membership
	syncAliasesFromPrimary(&next)
	if err := store.commit(next); err != nil {
		store.mu.Unlock()
		t.Fatal(err)
	}
	store.mu.Unlock()
	credential, err := store.IssueForOrganization(organization.ID, "portal:old", IssueInput{
		Label: "old recovery", ClusterIDs: []string{cluster.Cluster.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.EnsurePlatformOwner("portal:new", "kubeatlas:new", "New recovery"); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	migrated, ok := reopened.MemberForOrganization(organization.ID, "portal:new")
	if !ok || migrated.Role != "owner" || !migrated.LegacyRestricted ||
		!slices.Equal(migrated.ClusterIDs, []string{"secondary"}) ||
		!slices.Equal(migrated.KubernetesGroups, []string{compat.LegacyTierPrefix + "legacy"}) {
		t.Fatalf("replacement recovery membership = %#v, ok=%v", migrated, ok)
	}
	if _, ok := reopened.MemberForOrganization(organization.ID, "portal:old"); ok {
		t.Fatal("superseded recovery account retained organization membership")
	}
	if account, ok := reopened.Account("portal:old"); !ok || account.Status != "disabled" || account.Local || account.PlatformOwner {
		t.Fatalf("superseded recovery account = %#v, ok=%v", account, ok)
	}
	if _, err := reopened.Authenticate(credential.Token, "portal:old"); err == nil {
		t.Fatal("superseded recovery credential survived restart")
	}
}

func TestCanonicalAccountAndMembershipMutationsSurviveCommit(t *testing.T) {
	store, _ := testStore(t)
	if err := store.EnsurePlatformOwner("portal:recovery", "kubeatlas:recovery", "Recovery"); err != nil {
		t.Fatal(err)
	}
	id := "oidc:" + strings.Repeat("d", 64)
	if _, _, err := store.UpsertOIDCMember(id, "Drew"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdatePlatformUser(id, "active", []OrganizationMembership{{OrganizationID: "self-hosted", Role: "viewer"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdatePlatformUser(id, "disabled", nil); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	account, ok := reopened.Account(id)
	if !ok || account.Status != "disabled" {
		t.Fatalf("canonical account status was overwritten by an alias: %#v, ok=%v", account, ok)
	}
	if _, ok := reopened.MemberForOrganization("self-hosted", id); ok {
		t.Fatal("removed membership reappeared after commit and restart")
	}
}

func TestCreateOrganizationAcceptsConfiguredLocalRecoveryOwner(t *testing.T) {
	store, _ := testStore(t)
	if err := store.EnsurePlatformOwner("portal:recovery", "kubeatlas:recovery", "Recovery"); err != nil {
		t.Fatal(err)
	}
	organization, err := store.CreateOrganization("Recovery org", "portal:recovery")
	if err != nil {
		t.Fatal(err)
	}
	membership, ok := store.MemberForOrganization(organization.ID, "portal:recovery")
	if !ok || membership.Role != "owner" {
		t.Fatalf("created organization owner = %#v, ok=%v", membership, ok)
	}
}
