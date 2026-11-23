package hubstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "private", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	return s, &now
}

func cluster(t *testing.T, s *Store, id string) IssuedAgent {
	t.Helper()
	c, err := s.CreateCluster(ClusterInput{ID: id, Name: "Cluster " + id})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func issue(t *testing.T, s *Store, ttl int) IssuedCredential {
	t.Helper()
	c, err := s.Issue("owner", IssueInput{Label: "automation", ClusterIDs: []string{"alpha"}, ReadOnly: true, TTLSeconds: ttl})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func errorCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
	if err.Error() != "hub state: "+code {
		t.Fatal("error is not generic")
	}
}

func TestPersistenceDirectoryAndIsolation(t *testing.T) {
	s, _ := testStore(t)
	beta := cluster(t, s, "beta")
	alpha := cluster(t, s, "alpha")
	input := GroupInput{ID: "team", Name: "Team", ClusterIDs: []string{"beta", "alpha"}}
	g, err := s.SaveGroup(input)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(g.ClusterIDs, []string{"alpha", "beta"}) {
		t.Fatal("scope not canonical")
	}
	input.ClusterIDs[0], g.ClusterIDs[0] = "missing", "missing"
	if _, err := s.SaveGroup(GroupInput{ID: "empty", Name: "Empty"}); err != nil {
		t.Fatal(err)
	}
	p := issue(t, s, 0)
	p.Credential.ClusterIDs[0] = "missing"
	data, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{alpha.Token, beta.Token, p.Token} {
		if bytes.Contains(data, []byte(token)) {
			t.Fatal("raw credential persisted")
		}
	}
	for path, want := range map[string]os.FileMode{s.path: 0o600, filepath.Dir(s.path): 0o700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("incorrect mode: %v", err)
		}
	}
	reopened, err := OpenStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = s.now
	if got := reopened.Clusters(); len(got) != 2 || got[0].ID != "alpha" || got[1].ID != "beta" {
		t.Fatal("offline directory lost or not sorted")
	}
	if got, _ := reopened.Group("team"); !slices.Equal(got.ClusterIDs, []string{"alpha", "beta"}) {
		t.Fatal("group storage aliases caller")
	}
	if got, _ := reopened.Group("empty"); len(got.ClusterIDs) != 0 {
		t.Fatal("empty group acquired scope")
	}
	if _, err := reopened.AuthenticateAgent(alpha.Token, "alpha"); err != nil {
		t.Fatal(err)
	}
	grant, err := reopened.Authenticate(p.Token, "owner")
	if err != nil || !slices.Equal(grant.ClusterIDs, []string{"alpha"}) {
		t.Fatal("PAT did not survive restart")
	}
	metadata, err := json.Marshal([]any{reopened.Clusters(), reopened.Groups(), reopened.Credentials("owner")})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(metadata, []byte("digest")) || bytes.Contains(metadata, []byte(p.Token)) {
		t.Fatal("public metadata leaks authentication material")
	}
	for _, c := range reopened.data.Credentials {
		if bytes.Contains(metadata, []byte(c.Digest)) {
			t.Fatal("digest exposed")
		}
	}
	grant.ClusterIDs[0] = "beta"
	list := reopened.Credentials("owner")
	list[0].ClusterIDs[0] = "beta"
	groups := reopened.Groups()
	groups[1].ClusterIDs[0] = "beta"
	if got, _ := reopened.Authenticate(p.Token, "owner"); !slices.Equal(got.ClusterIDs, []string{"alpha"}) {
		t.Fatal("output aliases state")
	}
	if len(reopened.Credentials("other")) != 0 {
		t.Fatal("owner list leak")
	}
}

func TestAgentLifecycleAndGroups(t *testing.T) {
	s, now := testStore(t)
	a := cluster(t, s, "alpha")
	_, err := s.CreateCluster(a.Cluster.ClusterInput)
	errorCode(t, err, "conflict")
	_, err = s.UpdateCluster("alpha", ClusterInput{ID: "other", Name: "Other"})
	errorCode(t, err, "invalid_parameters")
	*now = now.Add(time.Minute)
	updated, err := s.UpdateCluster("alpha", ClusterInput{ID: "alpha", Name: "Renamed", Description: "Offline directory"})
	if err != nil || !updated.CreatedAt.Equal(a.Cluster.CreatedAt) || !updated.UpdatedAt.Equal(*now) {
		t.Fatal("metadata update lost identity")
	}
	if err := s.RevokeAgentToken("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAgentToken("alpha"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"alpha", "unknown"} {
		_, err := s.AuthenticateAgent(a.Token, id)
		errorCode(t, err, "credential_invalid")
	}
	c, _ := s.Cluster("alpha")
	*c.RevokedAt = time.Time{}
	if stored, _ := s.Cluster("alpha"); stored.RevokedAt.IsZero() {
		t.Fatal("cluster pointer aliases state")
	}
	_, err = s.RotateAgentToken("alpha")
	errorCode(t, err, "conflict")
	reenrolled, err := s.ReenrollCluster("self-hosted", "alpha", ClusterInput{ID: "alpha", Name: "Renamed", Description: "Offline directory"})
	if err != nil || reenrolled.Token == a.Token || reenrolled.Cluster.ID != "alpha" || reenrolled.Cluster.RevokedAt != nil {
		t.Fatalf("re-enrollment failed: %#v, %v", reenrolled, err)
	}
	_, err = s.AuthenticateAgent(a.Token, "alpha")
	errorCode(t, err, "credential_invalid")
	if _, err = s.AuthenticateAgent(reenrolled.Token, "alpha"); err != nil {
		t.Fatal(err)
	}
	for _, members := range [][]string{{"unknown"}, {"alpha", "alpha"}, {"*"}} {
		_, err = s.SaveGroup(GroupInput{ID: "bad", Name: "Bad", ClusterIDs: members})
		errorCode(t, err, "invalid_scope")
	}
	if _, err = s.SaveGroup(GroupInput{ID: "team", Name: "Team", ClusterIDs: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteGroup("team"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteGroup("team"); err != nil {
		t.Fatal(err)
	}
	if len(s.Groups()) != 0 {
		t.Fatal("group not deleted")
	}
}

func TestCredentialScopeOwnerAndTokenFamilies(t *testing.T) {
	s, _ := testStore(t)
	a := cluster(t, s, "alpha")
	cluster(t, s, "beta")
	for _, ids := range [][]string{nil, {}, {"*"}, {"unknown"}, {"alpha", "alpha"}} {
		_, err := s.Issue("owner", IssueInput{ClusterIDs: ids})
		errorCode(t, err, "invalid_scope")
	}
	for _, owner := range []string{"", "\n", "owner\x00"} {
		_, err := s.Issue(owner, IssueInput{ClusterIDs: []string{"alpha"}})
		errorCode(t, err, "invalid_parameters")
	}
	ids := []string{"beta", "alpha"}
	p, err := s.Issue("owner", IssueInput{ClusterIDs: ids})
	if err != nil || !slices.Equal(p.Credential.ClusterIDs, []string{"alpha", "beta"}) {
		t.Fatal("scope canonicalization failed")
	}
	ids[0] = "unknown"
	_, err = s.Authenticate(a.Token, "owner")
	errorCode(t, err, "credential_invalid")
	_, err = s.AuthenticateAgent(p.Token, "alpha")
	errorCode(t, err, "credential_invalid")
	_, err = s.Authenticate(p.Token, "changed-owner")
	errorCode(t, err, "credential_invalid")
	_, err = s.Refresh(p.Token, "changed-owner", 0)
	errorCode(t, err, "credential_invalid")
	_, err = s.Rotate("changed-owner", p.Credential.ID, 0)
	errorCode(t, err, "credential_invalid")
	errorCode(t, s.Revoke("changed-owner", p.Credential.ID), "credential_invalid")
	errorCode(t, s.Revoke("owner", "unknown"), "credential_invalid")
	if err = s.Revoke("owner", p.Credential.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Revoke("owner", p.Credential.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.Authenticate(p.Token, "owner")
	errorCode(t, err, "credential_revoked")
	_, err = s.Authenticate(p.Token, "other")
	errorCode(t, err, "credential_invalid")
	_, err = s.Authenticate(mcpPrefix+strings.Repeat("A", 43), "owner")
	errorCode(t, err, "credential_invalid")
	_, err = s.Rotate("owner", p.Credential.ID, 0)
	errorCode(t, err, "credential_revoked")
	listed := s.Credentials("owner")
	*listed[0].RevokedAt = time.Time{}
	if s.Credentials("owner")[0].RevokedAt.IsZero() {
		t.Fatal("revocation pointer alias")
	}
}

func TestTTLAndRefreshWindow(t *testing.T) {
	s, now := testStore(t)
	cluster(t, s, "alpha")
	for _, ttl := range []int{-1, minTTL - 1, maxTTL + 1, int(^uint(0) >> 1)} {
		_, err := s.Issue("owner", IssueInput{ClusterIDs: []string{"alpha"}, TTLSeconds: ttl})
		errorCode(t, err, "invalid_parameters")
	}
	p := issue(t, s, 0)
	if p.Credential.ExpiresAt.Sub(*now) != 8*time.Hour {
		t.Fatal("default TTL")
	}
	*now = p.Credential.ExpiresAt.Add(-time.Nanosecond)
	if _, err := s.Authenticate(p.Token, "owner"); err != nil {
		t.Fatal(err)
	}
	*now = p.Credential.ExpiresAt
	_, err := s.Authenticate(p.Token, "owner")
	errorCode(t, err, "credential_expired")
	_, err = s.Refresh(p.Token, "owner", 0)
	errorCode(t, err, "credential_expired")
	_, err = s.Rotate("owner", p.Credential.ID, 0)
	errorCode(t, err, "credential_expired")
	short := issue(t, s, minTTL)
	long, err := s.Refresh(short.Token, "owner", maxTTL)
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := s.Rotate("owner", long.Credential.ID, 0)
	if err != nil || inherited.Credential.ExpiresAt.Sub(*now) != 15*time.Minute {
		t.Fatal("renewal did not inherit original TTL")
	}
	_, err = s.Refresh(inherited.Token, "owner", maxTTL+1)
	errorCode(t, err, "invalid_parameters")
	p = issue(t, s, maxTTL)
	first := p.Credential
	for i := 0; i < 4; i++ {
		*now = now.Add(6 * 24 * time.Hour)
		previous := p
		p, err = s.Refresh(p.Token, "owner", 0)
		if err != nil {
			t.Fatal(err)
		}
		if p.Credential.ID == previous.Credential.ID || p.Token == previous.Token || !p.Credential.ReadOnly || !slices.Equal(p.Credential.ClusterIDs, first.ClusterIDs) || !p.Credential.RefreshUntil.Equal(first.RefreshUntil) {
			t.Fatal("renewal changed family grants")
		}
		_, err = s.Authenticate(previous.Token, "owner")
		errorCode(t, err, "credential_revoked")
	}
	if !p.Credential.ExpiresAt.Equal(first.RefreshUntil) {
		t.Fatal("expiry not capped to family window")
	}
	*now = first.RefreshUntil.Add(-15 * time.Minute)
	p, err = s.Rotate("owner", p.Credential.ID, 0)
	if err != nil || !p.Credential.ExpiresAt.Equal(first.RefreshUntil) {
		t.Fatal("exact minimum remaining window rejected")
	}
	*now = now.Add(time.Nanosecond)
	_, err = s.Refresh(p.Token, "owner", 0)
	errorCode(t, err, "credential_refresh_expired")
	if _, err = s.Authenticate(p.Token, "owner"); err != nil {
		t.Fatal("failed refresh revoked old token")
	}
	reopened, err := OpenStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = s.now
	if _, err = reopened.Authenticate(p.Token, "owner"); err != nil {
		t.Fatal(err)
	}
}

func TestNonExpiringCredentialSurvivesReopenAndPruning(t *testing.T) {
	s, now := testStore(t)
	cluster(t, s, "alpha")
	input := IssueInput{Label: "automation", ClusterIDs: []string{"alpha"}, ReadOnly: true, NeverExpires: true, TTLSeconds: minTTL}
	_, err := s.Issue("owner", input)
	errorCode(t, err, "invalid_parameters")
	input.TTLSeconds = 0
	issued, err := s.Issue("owner", input)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(familyLifetime + retention + time.Hour)
	reopened, err := OpenStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = s.now
	issue(t, reopened, minTTL)
	credential, err := reopened.Authenticate(issued.Token, "owner")
	if err != nil || !credential.NeverExpires || !credential.ExpiresAt.IsZero() || !credential.RefreshUntil.IsZero() {
		t.Fatalf("non-expiring credential did not survive reopen and pruning: %v", err)
	}
	_, err = reopened.Rotate("owner", credential.ID, minTTL)
	errorCode(t, err, "invalid_parameters")
	rotated, err := reopened.Rotate("owner", credential.ID, 0)
	if err != nil || !rotated.Credential.NeverExpires || !rotated.Credential.ReadOnly || !slices.Equal(rotated.Credential.ClusterIDs, credential.ClusterIDs) {
		t.Fatalf("rotation did not preserve credential lifetime and grants: %v", err)
	}
	_, err = reopened.Authenticate(issued.Token, "owner")
	errorCode(t, err, "credential_revoked")
	if err := reopened.Revoke("owner", rotated.Credential.ID); err != nil {
		t.Fatal(err)
	}
	_, err = reopened.Authenticate(rotated.Token, "owner")
	errorCode(t, err, "credential_revoked")
	*now = now.Add(retention + time.Duration(minTTL)*time.Second + time.Nanosecond)
	finite := issue(t, reopened, minTTL)
	listed := reopened.Credentials("owner")
	if len(listed) != 1 || listed[0].ID != finite.Credential.ID {
		t.Fatal("revoked non-expiring credentials should be pruned after retention")
	}
}

func TestConcurrentRefreshExactlyOneSuccess(t *testing.T) {
	s, _ := testStore(t)
	cluster(t, s, "alpha")
	p := issue(t, s, 0)
	var wg sync.WaitGroup
	results := make(chan IssuedCredential, 16)
	errors := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			c, err := s.Refresh(p.Token, "owner", 0)
			if err != nil {
				errors <- err
			} else {
				results <- c
			}
		})
	}
	wg.Wait()
	close(results)
	close(errors)
	if len(results) != 1 || len(errors) != 15 {
		t.Fatalf("successes=%d failures=%d", len(results), len(errors))
	}
	for err := range errors {
		errorCode(t, err, "credential_revoked")
	}
	for c := range results {
		if _, err := s.Authenticate(c.Token, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Authenticate(p.Token, "owner")
	errorCode(t, err, "credential_revoked")
	if len(s.Credentials("owner")) != 2 {
		t.Fatal("atomic refresh retained unexpected records")
	}
}

func TestFailedPersistenceIsAtomic(t *testing.T) {
	s, _ := testStore(t)
	a := cluster(t, s, "alpha")
	p := issue(t, s, 0)
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	original := s.path
	s.path = filepath.Join(filepath.Dir(original), "blocked")
	if err := os.Mkdir(s.path, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = s.Refresh(p.Token, "owner", 0)
	errorCode(t, err, "store_unavailable")
	failed, err := s.Issue("owner", IssueInput{ClusterIDs: []string{"alpha"}})
	errorCode(t, err, "store_unavailable")
	if failed.Token != "" || failed.Credential.ID != "" {
		t.Fatal("failed issue returned credentials")
	}
	_, err = s.Rotate("owner", p.Credential.ID, 0)
	errorCode(t, err, "store_unavailable")
	errorCode(t, s.Revoke("owner", p.Credential.ID), "store_unavailable")
	_, err = s.RotateAgentToken("alpha")
	errorCode(t, err, "store_unavailable")
	errorCode(t, s.RevokeAgentToken("alpha"), "store_unavailable")
	_, err = s.SaveGroup(GroupInput{ID: "team", Name: "Team"})
	errorCode(t, err, "store_unavailable")
	_, err = s.CreateCluster(ClusterInput{ID: "beta", Name: "Beta"})
	errorCode(t, err, "store_unavailable")
	_, err = s.UpdateCluster("alpha", ClusterInput{ID: "alpha", Name: "Changed"})
	errorCode(t, err, "store_unavailable")
	if _, err = s.Authenticate(p.Token, "owner"); err != nil {
		t.Fatal("old PAT changed on failure")
	}
	if _, err = s.AuthenticateAgent(a.Token, "alpha"); err != nil {
		t.Fatal("old agent token changed on failure")
	}
	if len(s.Credentials("owner")) != 1 || len(s.Clusters()) != 1 || len(s.Groups()) != 0 {
		t.Fatal("failed write changed memory")
	}
	if c, _ := s.Cluster("alpha"); c.Name != a.Cluster.Name {
		t.Fatal("failed update changed metadata")
	}
	after, err := os.ReadFile(original)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed write changed original disk state")
	}
	s.path = original
	if _, err = s.Refresh(p.Token, "owner", 0); err != nil {
		t.Fatal(err)
	}
}

func TestStrictStartupValidation(t *testing.T) {
	s, _ := testStore(t)
	cluster(t, s, "alpha")
	p := issue(t, s, 0)
	if _, err := s.SaveGroup(GroupInput{ID: "team", Name: "Team", ClusterIDs: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	base, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*storeFile){
		"version":    func(f *storeFile) { f.Version = 3 },
		"nil map":    func(f *storeFile) { f.Clusters = nil },
		"cluster ID": func(f *storeFile) { c := f.Clusters["alpha"]; c.ID = "other"; f.Clusters["alpha"] = c },
		"digest":     func(f *storeFile) { c := f.Credentials[p.Credential.ID]; c.Digest = "bad"; f.Credentials[c.ID] = c },
		"owner":      func(f *storeFile) { c := f.Credentials[p.Credential.ID]; c.OwnerID = ""; f.Credentials[c.ID] = c },
		"scope": func(f *storeFile) {
			c := f.Credentials[p.Credential.ID]
			c.ClusterIDs = []string{"unknown"}
			f.Credentials[c.ID] = c
		},
		"empty scope": func(f *storeFile) {
			c := f.Credentials[p.Credential.ID]
			c.ClusterIDs = []string{}
			f.Credentials[c.ID] = c
		},
		"TTL": func(f *storeFile) {
			c := f.Credentials[p.Credential.ID]
			c.TTLSeconds = maxTTL + 1
			f.Credentials[c.ID] = c
		},
		"family window": func(f *storeFile) {
			c := f.Credentials[p.Credential.ID]
			c.RefreshUntil = c.RefreshUntil.Add(time.Hour)
			f.Credentials[c.ID] = c
		},
		"expiry": func(f *storeFile) {
			c := f.Credentials[p.Credential.ID]
			c.ExpiresAt = c.CreatedAt
			f.Credentials[c.ID] = c
		},
		"revocation": func(f *storeFile) {
			c := f.Credentials[p.Credential.ID]
			past := c.CreatedAt.Add(-time.Hour)
			c.RevokedAt = &past
			f.Credentials[c.ID] = c
		},
		"cluster times": func(f *storeFile) {
			c := f.Clusters["alpha"]
			c.UpdatedAt = c.CreatedAt.Add(-time.Second)
			f.Clusters[c.ID] = c
		},
		"group members": func(f *storeFile) {
			g := f.Groups["team"]
			g.ClusterIDs = []string{"alpha", "alpha"}
			f.Groups[g.ID] = g
		},
		"family elevation": func(f *storeFile) {
			c := f.Credentials[p.Credential.ID]
			c.ID = strings.Repeat("a", 64)
			c.Digest = strings.Repeat("b", 64)
			c.ReadOnly = false
			f.Credentials[c.ID] = c
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var f storeFile
			if err := json.Unmarshal(base, &f); err != nil {
				t.Fatal(err)
			}
			mutate(&f)
			data, err := json.Marshal(f)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(s.path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = OpenStore(s.path)
			errorCode(t, err, "store_unavailable")
		})
	}
	for name, data := range map[string][]byte{
		"trailing":         append(slices.Clone(base), []byte(` {}`)...),
		"unknown":          bytes.Replace(base, []byte(`"version":2`), []byte(`"version":2,"extra":true`), 1),
		"duplicate":        bytes.Replace(base, []byte(`"version":2`), []byte(`"version":2,"version":1`), 1),
		"missing readonly": bytes.Replace(base, []byte(`"readOnly":true,`), nil, 1),
		"null readonly":    bytes.Replace(base, []byte(`"readOnly":true`), []byte(`"readOnly":null`), 1),
		"case alias":       bytes.Replace(base, []byte(`"readOnly":true`), []byte(`"readOnly":true,"ReadOnly":false`), 1),
		"invalid UTF8":     bytes.Replace(base, []byte(`"automation"`), []byte{'"', 0xff, '"'}, 1),
		"malformed":        []byte(`{"version":`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(s.path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := OpenStore(s.path)
			errorCode(t, err, "store_unavailable")
		})
	}
	file, err := os.OpenFile(s.path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(maxStoreBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = OpenStore(s.path)
	errorCode(t, err, "capacity_exceeded")
	if err = os.WriteFile(s.path, base, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(s.path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = OpenStore(s.path)
	errorCode(t, err, "store_unavailable")
	if err = os.Chmod(s.path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Dir(s.path), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = OpenStore(s.path)
	errorCode(t, err, "store_unavailable")
}

func TestCapacityAndRetention(t *testing.T) {
	s, now := testStore(t)
	cluster(t, s, "alpha")
	p := issue(t, s, 0)
	stored := s.data.Credentials[p.Credential.ID]
	for i := 1; len(s.data.Credentials) < maxCredentials; i++ {
		c := stored
		c.ID, c.FamilyID, c.Digest = fmt.Sprintf("%064x", i), fmt.Sprintf("%064x", i), fmt.Sprintf("%064x", i+maxCredentials)
		s.data.Credentials[c.ID] = c
	}
	_, err := s.Issue("owner", IssueInput{ClusterIDs: []string{"alpha"}})
	errorCode(t, err, "capacity_exceeded")
	_, err = s.Refresh(p.Token, "owner", 0)
	errorCode(t, err, "capacity_exceeded")
	if _, err = s.Authenticate(p.Token, "owner"); err != nil {
		t.Fatal(err)
	}
	*now = stored.ExpiresAt.Add(retention)
	if len(s.next(*now).Credentials) != maxCredentials {
		t.Fatal("pruned at inclusive retention boundary")
	}
	*now = now.Add(time.Nanosecond)
	if len(s.next(*now).Credentials) != 0 {
		t.Fatal("expired records not prunable")
	}
	issued := issue(t, s, 0)
	if len(s.Credentials("owner")) != 1 {
		t.Fatal("mutation did not prune eligible records")
	}
	*now = issued.Credential.ExpiresAt.Add(time.Hour)
	if err = s.Revoke("owner", issued.Credential.ID); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(retention)
	if len(s.next(*now).Credentials) != 1 {
		t.Fatal("late revocation retention shortened")
	}
	*now = now.Add(time.Nanosecond)
	if len(s.next(*now).Credentials) != 0 {
		t.Fatal("late revocation retention never expires")
	}
	before := s.data
	_, err = s.Issue(strings.Repeat("x", maxStoreBytes), IssueInput{ClusterIDs: []string{"alpha"}})
	errorCode(t, err, "capacity_exceeded")
	if !reflect.DeepEqual(before, s.data) {
		t.Fatal("byte-cap failure changed state")
	}
	for i := 1; len(s.data.Clusters) < maxClusters; i++ {
		c := s.data.Clusters["alpha"]
		c.ID = fmt.Sprintf("cluster-%d", i)
		c.Digest = fmt.Sprintf("%064x", i)
		s.data.Clusters[c.ID] = c
	}
	_, err = s.CreateCluster(ClusterInput{ID: "overflow", Name: "Overflow"})
	errorCode(t, err, "capacity_exceeded")
	for i := range maxGroups {
		id := fmt.Sprintf("group-%d", i)
		s.data.Groups[id] = Group{ID: id, Name: id, ClusterIDs: []string{}, CreatedAt: *now, UpdatedAt: *now}
	}
	_, err = s.SaveGroup(GroupInput{ID: "overflow", Name: "Overflow"})
	errorCode(t, err, "capacity_exceeded")
}

func TestNameWhitespaceValidation(t *testing.T) {
	for _, name := range []string{" Alpha", "Alpha ", "\u00a0Alpha\u00a0"} {
		t.Run(name, func(t *testing.T) {
			s, _ := testStore(t)
			cluster(t, s, "alpha")
			if _, err := s.SaveGroup(GroupInput{ID: "team", Name: "Alpha Team"}); err != nil {
				t.Fatal(err)
			}
			_, err := s.CreateCluster(ClusterInput{ID: "beta", Name: name})
			errorCode(t, err, "invalid_parameters")
			_, err = s.UpdateCluster("alpha", ClusterInput{ID: "alpha", Name: name})
			errorCode(t, err, "invalid_parameters")
			for _, id := range []string{"new-team", "team"} {
				_, err = s.SaveGroup(GroupInput{ID: id, Name: name})
				errorCode(t, err, "invalid_parameters")
			}
			for _, section := range []string{"clusters", "groups"} {
				file := s.next(s.now())
				if section == "clusters" {
					c := file.Clusters["alpha"]
					c.Name = name
					file.Clusters["alpha"] = c
				} else {
					g := file.Groups["team"]
					g.Name = name
					file.Groups["team"] = g
				}
				data, err := json.Marshal(file)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(s.path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				_, err = OpenStore(s.path)
				errorCode(t, err, "store_unavailable")
			}
		})
	}
	s, _ := testStore(t)
	if _, err := s.CreateCluster(ClusterInput{ID: "alpha", Name: "Alpha Cluster", Description: " description "}); err != nil {
		t.Fatal(err)
	}
	p, err := s.Issue(" owner ", IssueInput{Label: " label ", ClusterIDs: []string{"alpha"}})
	if err != nil || p.Credential.OwnerID != " owner " || p.Credential.Label != " label " {
		t.Fatal("non-name text semantics changed")
	}
	if _, err := OpenStore(s.path); err != nil {
		t.Fatal(err)
	}
}

func TestTextValidation(t *testing.T) {
	s, _ := testStore(t)
	for _, input := range []ClusterInput{
		{ID: "Bad", Name: "Name"}, {ID: "good", Name: ""}, {ID: "good", Name: strings.Repeat("n", 257)},
		{ID: "good", Name: "Line\nBreak"}, {ID: "good", Name: string([]byte{0xff})}, {ID: "good", Name: "Name", Description: strings.Repeat("d", 1001)},
		{ID: "good", Name: "Name", Environment: strings.Repeat("e", 129)}, {ID: "good", Name: "Name", Region: "x\x7f"},
	} {
		_, err := s.CreateCluster(input)
		errorCode(t, err, "invalid_parameters")
	}
}
