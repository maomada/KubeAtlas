package hubstate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthorizationRevisionTracksFrozenClockABAsAndPersists(t *testing.T) {
	store, _ := testStore(t)
	cluster(t, store, "alpha")
	cluster(t, store, "beta")
	id := "oidc:" + strings.Repeat("a", 64)
	if _, _, err := store.UpsertOIDCMember(id, "Revision User"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateMember(id, MemberInput{
		Role: "member", Status: "active", ClusterIDs: []string{"alpha"},
		KubernetesGroups: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	revision := accountRevision(t, store, id)
	if revision != 2 {
		t.Fatalf("initial active membership revision = %d, want 2", revision)
	}
	expectAdvance := func() {
		t.Helper()
		revision++
		if got := accountRevision(t, store, id); got != revision {
			t.Fatalf("authorization revision = %d, want %d", got, revision)
		}
	}
	update := func(input MemberInput) {
		t.Helper()
		if _, _, err := store.UpdateMember(id, input); err != nil {
			t.Fatal(err)
		}
		expectAdvance()
	}
	update(MemberInput{Role: "member", Status: "disabled", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{}})
	update(MemberInput{Role: "member", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{}})
	update(MemberInput{Role: "viewer", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{}})
	update(MemberInput{Role: "member", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{}})
	if _, err := store.RemoveOrganizationMember("self-hosted", id); err != nil {
		t.Fatal(err)
	}
	expectAdvance()
	update(MemberInput{Role: "member", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{}})
	update(MemberInput{Role: "member", Status: "active", ClusterIDs: []string{"beta"}, KubernetesGroups: []string{}})
	update(MemberInput{Role: "member", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{}})
	update(MemberInput{Role: "member", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{"kubeatlas:idp:reader"}})
	update(MemberInput{Role: "member", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{}})
	mutateRevisionMembership(t, store, id, func(membership *Membership) { membership.LegacyRestricted = false })
	expectAdvance()
	mutateRevisionMembership(t, store, id, func(membership *Membership) { membership.LegacyRestricted = true })
	expectAdvance()
	mutateRevisionMembership(t, store, id, func(membership *Membership) { membership.LegacyAllClusters = true })
	expectAdvance()
	mutateRevisionMembership(t, store, id, func(membership *Membership) { membership.LegacyAllClusters = false })
	expectAdvance()

	reopened, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := accountRevision(t, reopened, id); got != revision {
		t.Fatalf("restarted revision = %d, want %d", got, revision)
	}
}

func TestAuthorizationRevisionTracksAccountFieldsAndIgnoresDisplayMetadata(t *testing.T) {
	for _, test := range []struct {
		name   string
		update func(*Account)
	}{
		{name: "user ID", update: func(account *Account) { account.UserID = "kubeatlas:changed" }},
		{name: "email", update: func(account *Account) { account.Email = "revision@example.test" }},
		{name: "password hash", update: func(account *Account) { account.PasswordHash = "$2b$" + strings.Repeat("a", 56) }},
		{name: "status", update: func(account *Account) { account.Status = "active" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _ := testStore(t)
			id := "oidc:" + strings.Repeat("b", 64)
			if _, _, err := store.UpsertOIDCMember(id, "Revision User"); err != nil {
				t.Fatal(err)
			}
			mutateRevisionAccount(t, store, id, test.update)
			if got := accountRevision(t, store, id); got != 2 {
				t.Fatalf("authority field revision = %d, want 2", got)
			}
		})
	}

	store, _ := testStore(t)
	if err := store.EnsurePlatformOwner("portal:revision", "kubeatlas:revision", "Revision owner"); err != nil {
		t.Fatal(err)
	}
	mutateRevisionAccount(t, store, "portal:revision", func(account *Account) {
		account.PlatformOwner, account.Local = false, false
	})
	if got := accountRevision(t, store, "portal:revision"); got != 2 {
		t.Fatalf("platform-owner flags revision = %d, want 2", got)
	}

	id := "oidc:" + strings.Repeat("c", 64)
	if _, _, err := store.UpsertOIDCMember(id, "Revision User"); err != nil {
		t.Fatal(err)
	}
	mutateRevisionAccount(t, store, id, func(account *Account) {
		account.DisplayName = "Renamed"
		account.UpdatedAt = account.UpdatedAt.Add(1)
	})
	if got := accountRevision(t, store, id); got != 1 {
		t.Fatalf("display-only revision = %d, want 1", got)
	}
}

func TestAuthorizationRevisionIgnoresLookupsCredentialsNoopsAndOtherAccounts(t *testing.T) {
	store, _ := testStore(t)
	cluster(t, store, "alpha")
	id := "oidc:" + strings.Repeat("d", 64)
	if _, _, err := store.UpsertOIDCMember(id, "Revision User"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateMember(id, MemberInput{Role: "member", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{}}); err != nil {
		t.Fatal(err)
	}
	want := accountRevision(t, store, id)
	if _, ok := store.Account(id); !ok {
		t.Fatal("account lookup failed")
	}
	if _, ok := store.Member(id); !ok {
		t.Fatal("member lookup failed")
	}
	if _, ok := store.MemberForOrganization("self-hosted", id); !ok {
		t.Fatal("organization member lookup failed")
	}
	if _, _, err := store.UpsertOIDCMember(id, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := store.UpdateOrganizationMemberRole("self-hosted", id, "member"); err != nil || changed {
		t.Fatalf("same-role update changed=%v err=%v", changed, err)
	}
	if _, _, err := store.UpdateMember(id, MemberInput{Role: "member", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{}}); err != nil {
		t.Fatal(err)
	}
	issued, err := store.IssueForMember(id, IssueInput{Label: "revision", ClusterIDs: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshForMember(issued.Token, id, 3600); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	next := store.next(store.now().UTC())
	stale := next.Accounts[id]
	stale.AuthorizationRevision = 0
	next.Accounts[id] = stale
	err = store.commit(next)
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	otherID := "oidc:" + strings.Repeat("e", 64)
	if _, _, err := store.UpsertOIDCMember(otherID, "Other User"); err != nil {
		t.Fatal(err)
	}
	if got := accountRevision(t, store, id); got != want {
		t.Fatalf("stable revision = %d, want %d", got, want)
	}
	if got := accountRevision(t, store, otherID); got != 1 {
		t.Fatalf("new unrelated account revision = %d, want 1", got)
	}
}

func TestAuthorizationRevisionLegacyZeroAndFailedCommitAtomicity(t *testing.T) {
	store, _ := testStore(t)
	id := "oidc:" + strings.Repeat("f", 64)
	if _, _, err := store.UpsertOIDCMember(id, "Legacy User"); err != nil {
		t.Fatal(err)
	}
	persisted, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(persisted, &file); err != nil {
		t.Fatal(err)
	}
	var accounts map[string]json.RawMessage
	if err := json.Unmarshal(file["accounts"], &accounts); err != nil {
		t.Fatal(err)
	}
	var account map[string]json.RawMessage
	if err := json.Unmarshal(accounts[id], &account); err != nil {
		t.Fatal(err)
	}
	delete(account, "authorizationRevision")
	accounts[id], err = json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	file["accounts"], err = json.Marshal(accounts)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := accountRevision(t, reopened, id); got != 0 {
		t.Fatalf("legacy absent revision = %d, want 0", got)
	}

	activeID := "oidc:" + strings.Repeat("1", 64)
	cluster(t, reopened, "alpha")
	if _, _, err := reopened.UpsertOIDCMember(activeID, "Active User"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reopened.UpdateMember(activeID, MemberInput{Role: "member", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(reopened.path)
	if err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(filepath.Dir(reopened.path), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalPath := reopened.path
	reopened.path = filepath.Join(blocker, "state.json")
	_, _, updateErr := reopened.UpdateMember(activeID, MemberInput{Role: "viewer", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{}})
	reopened.path = originalPath
	errorCode(t, updateErr, "store_unavailable")
	after, err := os.ReadFile(reopened.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed commit changed persisted state")
	}
	if got := accountRevision(t, reopened, activeID); got != 2 {
		t.Fatalf("failed commit changed in-memory revision to %d", got)
	}
	member, ok := reopened.MemberForOrganization("self-hosted", activeID)
	if !ok || member.Role != "member" {
		t.Fatalf("failed commit changed in-memory membership: %#v", member)
	}
}

func TestAuthorizationRevisionOverflowRejectsMutationBeforeWrite(t *testing.T) {
	store, _ := testStore(t)
	cluster(t, store, "alpha")
	id := "oidc:" + strings.Repeat("2", 64)
	if _, _, err := store.UpsertOIDCMember(id, "Overflow User"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateMember(id, MemberInput{Role: "member", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: []string{}}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	account := store.data.Accounts[id]
	account.AuthorizationRevision = ^uint64(0)
	store.data.Accounts[id] = account
	err := store.commit(store.next(store.now().UTC()))
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.UpdateOrganizationMemberRole("self-hosted", id, "viewer")
	errorCode(t, err, "capacity_exceeded")
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("overflow mutation changed persisted state")
	}
	if got := accountRevision(t, store, id); got != ^uint64(0) {
		t.Fatalf("overflow mutation changed in-memory revision to %d", got)
	}
	member, ok := store.MemberForOrganization("self-hosted", id)
	if !ok || member.Role != "member" {
		t.Fatalf("overflow mutation changed in-memory membership: %#v", member)
	}
}

func TestCommitRejectsNilAccountsMap(t *testing.T) {
	store, _ := testStore(t)
	if err := store.EnsurePlatformOwner("portal:nil-accounts", "kubeatlas:nil-accounts", "Nil Accounts"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	next := store.next(store.now().UTC())
	next.Accounts = nil
	err = store.commit(next)
	store.mu.Unlock()
	errorCode(t, err, "store_unavailable")
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("nil account map commit changed persisted state")
	}
	if got := accountRevision(t, store, "portal:nil-accounts"); got != 1 {
		t.Fatalf("nil account map commit changed live revision to %d", got)
	}
}

func accountRevision(t *testing.T, store *Store, id string) uint64 {
	t.Helper()
	account, ok := store.Account(id)
	if !ok {
		t.Fatalf("account %q not found", id)
	}
	return account.AuthorizationRevision
}

func mutateRevisionAccount(t *testing.T, store *Store, id string, mutate func(*Account)) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	next := store.next(store.now().UTC())
	account := next.Accounts[id]
	mutate(&account)
	next.Accounts[id] = account
	if err := store.commit(next); err != nil {
		t.Fatal(err)
	}
}

func mutateRevisionMembership(t *testing.T, store *Store, id string, mutate func(*Membership)) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	next := store.next(store.now().UTC())
	membership := next.Memberships["self-hosted"][id]
	mutate(&membership)
	next.Memberships["self-hosted"][id] = membership
	if err := store.commit(next); err != nil {
		t.Fatal(err)
	}
}
