package fleetsync

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func sampleCluster(id string, now time.Time) Cluster {
	return Cluster{Snapshot: Snapshot{Version: Version, ClusterID: id, Name: id, SourceID: "source-" + id, ObservedAt: now,
		Documents: []Document{
			{Tool: "get_topology", Input: json.RawMessage(`{"view":"resources"}`), Data: json.RawMessage(`{"nodes":[{"id":"pod/default/api"}],"edges":[]}`)},
			{Tool: "get_resource", Input: json.RawMessage(`{"kind":"pods","namespace":"default","name":"api"}`), Data: json.RawMessage(`{"resource":{"metadata":{"name":"api"}},"resourceContext":{"tier":"basic","owner":{"kind":"ReplicaSet","name":"api"}}}`)},
		}, Limitations: []string{}}, ReceivedAt: now}
}

func TestContextSelectionAndCanonicalPayload(t *testing.T) {
	now := time.Now().UTC()
	a, b := sampleCluster("east", now), sampleCluster("west", now)
	clusters := map[string]Cluster{"east": a, "west": b}
	groups := map[string]Group{"prod": {ID: "prod", Name: "Production", ClusterIDs: []string{"west", "east"}}}
	for _, input := range []Selection{{}, {ClusterID: "east", FleetID: "prod"}, {ClusterID: "unknown"}, {FleetID: "unknown"}} {
		if _, err := AssembleContext(input, clusters, groups, now); err == nil {
			t.Fatalf("accepted ambiguous/unknown selection: %+v", input)
		}
	}
	single, err := AssembleContext(Selection{ClusterID: "east"}, clusters, groups, now)
	if err != nil || single.Mode != "single" || len(single.Clusters) != 1 || !reflect.DeepEqual(single.Clusters[0].Documents[0], a.Documents[0]) {
		t.Fatalf("single-cluster payload changed: %+v %v", single, err)
	}
	multi, err := AssembleContext(Selection{FleetID: "prod"}, clusters, groups, now)
	if err != nil || multi.Mode != "multi_cloud" || len(multi.Clusters) != 2 || multi.Clusters[0].ClusterID != "east" || multi.Clusters[1].ClusterID != "west" {
		t.Fatalf("unexpected group context: %+v %v", multi, err)
	}
	if !reflect.DeepEqual(multi.Clusters[0].Documents, multi.Clusters[1].Documents) {
		t.Fatal("canonical same-named objects should be preserved in separate cluster envelopes")
	}
	resource, err := ResourceContext(ResourceSelection{ClusterID: "east", Kind: "pods", Namespace: "default", Name: "api"}, a, now)
	if err != nil || !reflect.DeepEqual(resource.Documents[0].Data, a.Documents[1].Data) {
		t.Fatalf("canonical resourceContext changed: %+v %v", resource, err)
	}
	if _, err := ResourceContext(ResourceSelection{ClusterID: "west", Kind: "pods", Namespace: "default", Name: "api"}, a, now); err == nil {
		t.Fatal("resource served from wrong cluster")
	}
}

func TestContextMissingStaleAndMembershipChange(t *testing.T) {
	now := time.Now().UTC()
	clusters := map[string]Cluster{"east": sampleCluster("east", now.Add(-StaleAfter-time.Second))}
	groups := map[string]Group{"prod": {ID: "prod", Name: "prod", ClusterIDs: []string{"east", "missing"}}}
	result, err := AssembleContext(Selection{FleetID: "prod"}, clusters, groups, now)
	if err != nil || result.Clusters[0].State != "stale" || result.Clusters[1].State != "unavailable" {
		t.Fatalf("missing/stale data hidden: %+v %v", result, err)
	}
	groups["prod"] = Group{ID: "prod", Name: "prod", ClusterIDs: []string{"east"}}
	result, _ = AssembleContext(Selection{FleetID: "prod"}, clusters, groups, now)
	if len(result.Clusters) != 1 {
		t.Fatal("removed member still included")
	}
	result.Clusters[0].Documents[0].Data[0] = 'x'
	if clusters["east"].Documents[0].Data[0] != '{' {
		t.Fatal("context aliases stored payload")
	}
}

func TestSnapshotValidation(t *testing.T) {
	now := time.Now().UTC()
	snapshot := sampleCluster("east", now).Snapshot
	if err := snapshot.Validate(now); err != nil {
		t.Fatal(err)
	}
	snapshot.Documents = append(snapshot.Documents, snapshot.Documents[0])
	if err := snapshot.Validate(now); err == nil {
		t.Fatal("accepted duplicate document")
	}
	snapshot = sampleCluster("east", now).Snapshot
	snapshot.Documents[0].Error = "failed"
	if err := snapshot.Validate(now); err == nil {
		t.Fatal("accepted failed read with successful payload")
	}
}
