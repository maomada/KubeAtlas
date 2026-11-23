package hubstate

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func testLocalPasswordHash(t *testing.T, input string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(input), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(hash)
}

func TestLocalPlatformOwnerConcurrentSetupPersistsOneIdentity(t *testing.T) {
	store, _ := testStore(t)
	hash := testLocalPasswordHash(t, "setup test input")
	type result struct {
		account Account
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var group sync.WaitGroup
	for _, username := range []string{"first", "second"} {
		group.Go(func() {
			<-start
			account, err := store.SetLocalPlatformOwner(username, hash, false)
			results <- result{account: account, err: err}
		})
	}
	close(start)
	group.Wait()
	close(results)
	var winner Account
	successes := 0
	for result := range results {
		if result.err != nil {
			errorCode(t, result.err, "already_initialized")
			continue
		}
		winner = result.account
		successes++
	}
	if successes != 1 {
		t.Fatalf("successful setup attempts = %d, want 1", successes)
	}
	id := fmt.Sprintf("portal:%x", sha256.Sum256([]byte(winner.DisplayName)))
	if winner.ID != id || winner.UserID != "kubeatlas:"+id || !winner.Local || !winner.PlatformOwner {
		t.Fatal("new recovery account did not receive the expected identity")
	}
	reopened, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if owner, ok := reopened.LocalPlatformOwner(); !ok || owner != winner {
		t.Fatal("saved recovery account differs after reopening the store")
	}
	if member, ok := reopened.MemberForOrganization("self-hosted", id); !ok || member.Role != "owner" {
		t.Fatal("new recovery identity has no default owner membership")
	}
}

func TestLocalPlatformOwnerResetKeepsRecoveryIdentityAndAgentRegistration(t *testing.T) {
	store, _ := testStore(t)
	const id, userID, username = "portal:legacy", "kubeatlas:portal:legacy", "legacy-admin"
	if err := store.EnsurePlatformOwner(id, userID, username); err != nil {
		t.Fatal(err)
	}
	organization, err := store.CreateOrganization("Secondary organization", id)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := store.CreateClusterForOrganization(organization.ID, ClusterInput{ID: "managed", Name: "Managed"})
	if err != nil {
		t.Fatal(err)
	}
	membership := cloneMembership(store.data.Memberships[organization.ID][id])
	firstHash := testLocalPasswordHash(t, "legacy test input")
	secondHash := testLocalPasswordHash(t, "replacement test input")
	if _, err := store.SetLocalPlatformOwner("unrelated", firstHash, false); err == nil {
		t.Fatal("legacy password migration accepted a different username")
	} else {
		errorCode(t, err, "already_initialized")
	}
	legacy, ok := store.LocalPlatformOwner()
	if !ok || legacy.PasswordHash != "" || legacy.ID != id {
		t.Fatal("rejected migration changed the legacy owner")
	}
	migrated, err := store.SetLocalPlatformOwner(username, firstHash, false)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.ID != id || migrated.UserID != userID || migrated.PasswordHash != firstHash {
		t.Fatal("legacy password migration replaced the recovery identity")
	}
	if _, err := store.SetLocalPlatformOwner(username, secondHash, false); err == nil {
		t.Fatal("initialization replaced an existing password")
	} else {
		errorCode(t, err, "already_initialized")
	}
	if err := store.EnsurePlatformOwner(id, userID, username); err != nil {
		t.Fatal(err)
	}
	if unchanged, _ := store.LocalPlatformOwner(); unchanged != migrated {
		t.Fatal("legacy EnsurePlatformOwner discarded persisted credentials")
	}
	if err := store.EnsurePlatformOwner("portal:other", "kubeatlas:portal:other", "other"); err == nil {
		t.Fatal("legacy EnsurePlatformOwner displaced a persisted recovery account")
	} else {
		errorCode(t, err, "already_initialized")
	}
	renamed, err := store.SetLocalPlatformOwner("renamed-admin", secondHash, true)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != id || renamed.UserID != userID || renamed.DisplayName != "renamed-admin" ||
		renamed.PasswordHash != secondHash || renamed.AuthorizationRevision <= migrated.AuthorizationRevision {
		t.Fatal("explicit reset did not preserve identity and advance the authorization revision")
	}
	reopened, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if owner, ok := reopened.LocalPlatformOwner(); !ok || owner != renamed {
		t.Fatal("renamed recovery account differs after reopening the store")
	}
	if current := reopened.data.Memberships[organization.ID][id]; !reflect.DeepEqual(current, membership) {
		t.Fatal("explicit reset changed the owner's organization membership")
	}
	if cluster, err := reopened.AuthenticateAgent(enrollment.Token, enrollment.Cluster.ID); err != nil ||
		cluster.OrganizationID != organization.ID {
		t.Fatal("explicit reset changed the existing Agent registration")
	}
}
