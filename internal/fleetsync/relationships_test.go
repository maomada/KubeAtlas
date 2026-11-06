package fleetsync

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRelationshipExtractionAndFederationScope(t *testing.T) {
	now := time.Now().UTC()
	observe := func(kind, group, name, data string) []RelationshipFact {
		return ExtractRelationshipFacts(EvidenceResource{Kind: kind, Group: group, Name: name}, json.RawMessage(data))
	}
	member := observe("clusters", "cluster.karmada.io", "worker", `{"spec":{"id":"worker-uid"}}`)
	identity := observe("namespaces", "", "kube-system", `{"metadata":{"uid":"worker-uid"}}`)
	control := sampleCluster("control", now)
	worker := sampleCluster("worker", now)
	control.RelationshipFacts = append(member, observe("resourcebindings", "work.karmada.io", "api", `{"spec":{"clusters":[{"name":"worker"}]}}`)...)
	worker.RelationshipFacts = identity
	clusters := map[string]Cluster{"control": control, "worker": worker}
	result := AssembleRelationships([]string{"control", "worker"}, clusters, now)
	if result.State != "declared_membership_observed" || len(result.Relationships) != 2 {
		t.Fatalf("membership and distribution = %+v", result)
	}
	for _, rel := range result.Relationships {
		if rel.State != "declared" || len(rel.Targets) != 1 || rel.Targets[0].ClusterID != "worker" {
			t.Fatalf("relationship = %+v", rel)
		}
	}
	result = AssembleRelationships([]string{"control"}, clusters, now)
	if result.State != "unknown" || len(result.Relationships[0].Targets) != 0 {
		t.Fatalf("out-of-group target leaked: %+v", result)
	}
	worker.ObservedAt = now.Add(-StaleAfter - time.Second)
	clusters["worker"] = worker
	result = AssembleRelationships([]string{"control", "worker"}, clusters, now)
	if result.Relationships[0].State != "stale_evidence" || result.State == "declared_membership_observed" {
		t.Fatalf("stale evidence promoted: %+v", result)
	}
	duplicate := sampleCluster("duplicate", now)
	duplicate.RelationshipFacts = identity
	clusters["duplicate"] = duplicate
	result = AssembleRelationships([]string{"control", "worker", "duplicate"}, clusters, now)
	if result.Relationships[0].State != "ambiguous" {
		t.Fatalf("duplicate identity merged: %+v", result)
	}
}

func TestIPReferenceDoesNotTurnOverlapIntoFederation(t *testing.T) {
	now := time.Now().UTC()
	ref := EvidenceResource{Kind: "pods", Name: "api", Namespace: "default"}
	addresses := ExtractRelationshipFacts(ref, json.RawMessage(`{"status":{"podIP":"10.0.0.2","podIPs":[{"ip":"10.0.0.2"}]}}`))
	if len(addresses) != 1 {
		t.Fatalf("duplicate pod addresses: %+v", addresses)
	}
	east, west := sampleCluster("east", now), sampleCluster("west", now)
	east.RelationshipFacts, west.RelationshipFacts = addresses, addresses
	clusters := map[string]Cluster{"east": east, "west": west}
	result := AssembleRelationships([]string{"east", "west"}, clusters, now)
	if len(result.Relationships) != 0 || result.State != "unknown" {
		t.Fatalf("overlap alone became relationship: %+v", result)
	}
	east.RelationshipFacts = append(east.RelationshipFacts, ExtractRelationshipFacts(EvidenceResource{Kind: "endpointslices", Group: "discovery.k8s.io", Name: "api"}, json.RawMessage(`{"endpoints":[{"addresses":["10.0.0.2","invalid","127.0.0.1"]}]}`))...)
	clusters["east"] = east
	result = AssembleRelationships([]string{"east", "west"}, clusters, now)
	if len(result.Relationships) != 1 || result.Relationships[0].State != "ambiguous" || result.State != "candidates_only" {
		t.Fatalf("overlapping reference = %+v", result)
	}
	east.RelationshipFacts = east.RelationshipFacts[1:]
	clusters["east"] = east
	result = AssembleRelationships([]string{"east", "west", "missing"}, clusters, now)
	if result.Relationships[0].State != "candidate" || result.Coverage[1].State != "unavailable" {
		t.Fatalf("candidate/missing = %+v", result)
	}
}

func TestRelationshipContextPreservesSingleClusterPayloads(t *testing.T) {
	now := time.Now().UTC()
	cluster := sampleCluster("east", now)
	cluster.RelationshipFacts = []RelationshipFact{{Type: "address", Value: "10.0.0.1", Resource: EvidenceResource{Kind: "pods", Name: "same"}}}
	result, err := AssembleContext(Selection{ClusterID: "east"}, map[string]Cluster{"east": cluster}, nil, now)
	if err != nil || result.Federation != nil {
		t.Fatalf("single context changed: %+v %v", result, err)
	}
	clone := cloneCluster(cluster)
	clone.RelationshipFacts[0].Value = "10.0.0.2"
	if cluster.RelationshipFacts[0].Value != "10.0.0.1" {
		t.Fatal("facts share backing array")
	}
}
