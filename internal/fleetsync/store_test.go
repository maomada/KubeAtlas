package fleetsync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func testStatePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "fleet-state", "state.json")
}

func TestStorePersistenceAndCopyIsolation(t *testing.T) {
	path := testStatePath(t)
	now := time.Now().UTC()
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := sampleCluster("east", now).Snapshot
	if err := store.Ingest(snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Documents[0].Data[0] = 'x'
	groups := []Group{{ID: "prod", Name: "Production", ClusterIDs: []string{"east"}}}
	if err := store.PutGroup(groups[0]); err != nil {
		t.Fatal(err)
	}
	groups[0].ClusterIDs[0] = "changed"
	if got := store.Groups()[0].ClusterIDs[0]; got != "east" {
		t.Fatalf("group aliases caller memory: %q", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, err = %v", info.Mode().Perm(), err)
	}

	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	context, err := reopened.Context(Selection{FleetID: "prod"})
	if err != nil || len(context.Clusters) != 1 || context.Clusters[0].ClusterID != "east" {
		t.Fatalf("persisted context = %+v, err = %v", context, err)
	}
	context.Clusters[0].Documents[0].Data[0] = 'x'
	inventory := reopened.Inventory()
	if len(inventory) != 1 || len(inventory[0].Documents) != 0 {
		t.Fatal("inventory must not expose snapshot documents")
	}
	resource, err := reopened.Resource(ResourceSelection{ClusterID: "east", Kind: "pods", Namespace: "default", Name: "api"})
	if err != nil || resource.Documents[0].Data[0] != '{' {
		t.Fatalf("stored data was mutated by returned context: %+v, err = %v", resource, err)
	}
}

func TestStoreGroupsIncludeUnavailableMembersAndReflectEdits(t *testing.T) {
	store, err := OpenStore(testStatePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.Ingest(sampleCluster("east", now).Snapshot); err != nil {
		t.Fatal(err)
	}
	group := Group{ID: "prod", Name: "Production", ClusterIDs: []string{"east", "pending"}}
	if err := store.PutGroup(group); err != nil {
		t.Fatal(err)
	}
	got, err := store.Context(Selection{FleetID: "prod"})
	if err != nil || len(got.Clusters) != 2 || got.Clusters[0].ClusterID != "east" || got.Clusters[1].State != "unavailable" {
		t.Fatalf("context with unavailable member = %+v, err = %v", got, err)
	}
	group.Name = "Updated"
	group.ClusterIDs = []string{"pending"}
	if err := store.PutGroup(group); err != nil {
		t.Fatal(err)
	}
	got, err = store.Context(Selection{FleetID: "prod"})
	if err != nil || got.Fleet.Name != "Updated" || len(got.Clusters) != 1 || got.Clusters[0].State != "unavailable" {
		t.Fatalf("edited group context = %+v, err = %v", got, err)
	}
	if groups := store.Groups(); len(groups) != 1 || groups[0].Name != "Updated" {
		t.Fatalf("stored groups = %+v", groups)
	}
	if err := store.DeleteGroup("prod"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Context(Selection{FleetID: "prod"}); err == nil {
		t.Fatal("deleted group was still selectable")
	}
}

func TestStoreSnapshotReplacementAndFailureDoNotKeepOldDocuments(t *testing.T) {
	store, err := OpenStore(testStatePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := sampleCluster("east", time.Now().UTC()).Snapshot
	if err := store.Ingest(first); err != nil {
		t.Fatal(err)
	}
	second := Snapshot{
		Version: Version, ClusterID: "east", Name: "east", SourceID: first.SourceID,
		ObservedAt: first.ObservedAt.Add(time.Second),
		Documents: []Document{{
			Tool: "list_resources", Input: json.RawMessage(`{"kind":"pods"}`), Error: "read unavailable",
		}},
		Limitations: []string{"partial replacement"},
	}
	if err := store.Ingest(second); err != nil {
		t.Fatal(err)
	}
	result, err := store.Context(Selection{ClusterID: "east"})
	if err != nil || len(result.Clusters[0].Documents) != 1 || result.Clusters[0].Documents[0].Tool != "list_resources" {
		t.Fatalf("replacement retained old documents: %+v, err = %v", result, err)
	}
	if _, err := store.Resource(ResourceSelection{ClusterID: "east", Kind: "pods", Namespace: "default", Name: "api"}); err == nil {
		t.Fatal("resource success from an older snapshot survived failed replacement")
	}
}

func TestStoreReplayOrderingAndSourceIdentity(t *testing.T) {
	store, err := OpenStore(testStatePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := sampleCluster("east", time.Now().UTC()).Snapshot
	if err := store.Ingest(snapshot); err != nil {
		t.Fatal(err)
	}
	receivedAt := store.Inventory()[0].ReceivedAt
	if err := store.Ingest(cloneSnapshot(snapshot)); err != nil {
		t.Fatalf("identical retry rejected: %v", err)
	}
	if got := store.Inventory()[0].ReceivedAt; !got.Equal(receivedAt) {
		t.Fatalf("identical retry refreshed ReceivedAt: %v -> %v", receivedAt, got)
	}
	different := cloneSnapshot(snapshot)
	different.Documents[0].Data = json.RawMessage(`{"different":true}`)
	if err := store.Ingest(different); err == nil {
		t.Fatal("same-timestamp changed payload was accepted")
	}
	older := cloneSnapshot(snapshot)
	older.ObservedAt = older.ObservedAt.Add(-time.Second)
	if err := store.Ingest(older); err == nil {
		t.Fatal("older snapshot was accepted")
	}
	wrongSource := cloneSnapshot(snapshot)
	wrongSource.SourceID = "different-source"
	wrongSource.ObservedAt = wrongSource.ObservedAt.Add(time.Second)
	if err := store.Ingest(wrongSource); err == nil {
		t.Fatal("changed source identity was accepted")
	}
}

func TestStoreRejectsInvalidStateAndGroups(t *testing.T) {
	store, err := OpenStore(testStatePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, group := range []Group{
		{ID: "bad_id", Name: "bad"},
		{ID: "prod", Name: ""},
		{ID: "prod", Name: "Production", ClusterIDs: []string{"east", "east"}},
		{ID: "prod", Name: "Production", ClusterIDs: []string{"bad_id"}},
	} {
		if err := store.PutGroup(group); err == nil {
			t.Fatalf("accepted invalid group %+v", group)
		}
	}
	if got := store.Groups(); !reflect.DeepEqual(got, []Group{}) {
		t.Fatalf("invalid groups changed state: %+v", got)
	}
}

func TestStoreRejectsNonStrictOrOversizedPersistedState(t *testing.T) {
	for name, data := range map[string][]byte{
		"unknown field":  []byte(`{"version":1,"clusters":[],"groups":[],"extra":true}`),
		"trailing value": []byte(`{"version":1,"clusters":[],"groups":[]} {}`),
	} {
		t.Run(name, func(t *testing.T) {
			path := testStatePath(t)
			if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := OpenStore(path)
			if err == nil {
				_ = store.Close()
				t.Fatal("invalid state was accepted")
			}
		})
	}
}
