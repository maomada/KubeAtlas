package fleetsync

import (
	"encoding/json"
	"net/netip"
	"slices"
	"strings"
	"time"
)

type EvidenceResource struct {
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

type RelationshipFact struct {
	Type       string           `json:"type"`
	Resource   EvidenceResource `json:"resource"`
	Value      string           `json:"value"`
	MemberName string           `json:"memberName,omitempty"`
}

type RelationshipTarget struct {
	ClusterID  string           `json:"clusterId"`
	Resource   EvidenceResource `json:"resource"`
	ObservedAt time.Time        `json:"observedAt"`
	State      string           `json:"state"`
}

type Relationship struct {
	Type     string               `json:"type"`
	State    string               `json:"state"`
	Source   RelationshipTarget   `json:"source"`
	Targets  []RelationshipTarget `json:"targets"`
	Evidence RelationshipFact     `json:"evidence"`
}

type RelationshipCoverage struct {
	ClusterID string `json:"clusterId"`
	State     string `json:"state"`
	Facts     int    `json:"facts"`
}

type FederationContext struct {
	State         string                 `json:"state"`
	Relationships []Relationship         `json:"relationships"`
	Coverage      []RelationshipCoverage `json:"coverage"`
	Truncated     bool                   `json:"truncated"`
	Limitations   []string               `json:"limitations"`
}

// Detail MCP projections strip UIDs and Pod IPs. Source-side extraction runs
// on the original object instead; the canonical MCP document is not modified.
func ExtractRelationshipFacts(ref EvidenceResource, data json.RawMessage) []RelationshipFact {
	var object map[string]any
	if json.Unmarshal(data, &object) != nil {
		return nil
	}
	if wrapped, ok := object["resource"].(map[string]any); ok {
		object = wrapped
	}
	var facts []RelationshipFact
	seen := map[string]bool{}
	add := func(kind, value, member string) {
		if kind == "address" || kind == "ip_reference" {
			ip, err := netip.ParseAddr(value)
			if err != nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
				return
			}
			value = ip.Unmap().String()
		}
		key := kind + "\x00" + value + "\x00" + member
		if value != "" && !seen[key] && len(facts) < 512 {
			seen[key] = true
			facts = append(facts, RelationshipFact{Type: kind, Resource: ref, Value: value, MemberName: member})
		}
	}
	switch {
	case ref.Group == "" && ref.Kind == "namespaces" && ref.Name == "kube-system":
		add("cluster_identity", fieldString(object, "metadata", "uid"), "")
	case ref.Group == "" && ref.Kind == "pods":
		add("address", fieldString(object, "status", "podIP"), "")
		for _, item := range fieldArray(object, "status", "podIPs") {
			add("address", fieldString(item, "ip"), "")
		}
	case ref.Group == "" && ref.Kind == "nodes":
		for _, item := range fieldArray(object, "status", "addresses") {
			add("address", fieldString(item, "address"), "")
		}
	case ref.Group == "" && ref.Kind == "services":
		add("address", fieldString(object, "spec", "clusterIP"), "")
		for _, key := range []string{"clusterIPs", "externalIPs"} {
			for _, item := range fieldArray(object, "spec", key) {
				value, _ := item.(string)
				add("address", value, "")
			}
		}
		for _, item := range fieldArray(object, "status", "loadBalancer", "ingress") {
			add("address", fieldString(item, "ip"), "")
		}
	case ref.Group == "" && ref.Kind == "endpoints":
		for _, subset := range fieldArray(object, "subsets") {
			for _, key := range []string{"addresses", "notReadyAddresses"} {
				for _, item := range fieldArray(subset, key) {
					add("ip_reference", fieldString(item, "ip"), "")
				}
			}
		}
	case ref.Group == "discovery.k8s.io" && ref.Kind == "endpointslices":
		for _, endpoint := range fieldArray(object, "endpoints") {
			for _, item := range fieldArray(endpoint, "addresses") {
				value, _ := item.(string)
				add("ip_reference", value, "")
			}
		}
	case ref.Group == "cluster.karmada.io" && ref.Kind == "clusters":
		add("federation_member", fieldString(object, "spec", "id"), ref.Name)
	case ref.Group == "work.karmada.io" && (ref.Kind == "resourcebindings" || ref.Kind == "clusterresourcebindings"):
		for _, item := range fieldArray(object, "spec", "clusters") {
			member := fieldString(item, "name")
			add("distribution", member, member)
		}
	}
	return facts
}

func field(value any, path ...string) any {
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}

func fieldString(value any, path ...string) string {
	result, _ := field(value, path...).(string)
	return result
}
func fieldArray(value any, path ...string) []any {
	result, _ := field(value, path...).([]any)
	return result
}

func AssembleRelationships(ids []string, clusters map[string]Cluster, now time.Time) *FederationContext {
	result := &FederationContext{State: "unknown", Relationships: []Relationship{}, Coverage: []RelationshipCoverage{}, Limitations: []string{
		"Inference covers synchronized relationship facts only; missing facts do not prove absence. Snapshot limits and per-cluster limitations apply.",
		"IP references are candidates, not proof of connectivity or federation; overlapping networks, NAT and shared addresses can be ambiguous.",
		"Karmada spec.id matched to kube-system UID is declared membership, not physical identity proof. Distribution is a declaration, not successful delivery.",
		"No automatic membership changes or resource merging. Other federation controllers and DNS-only relationships are not inferred yet.",
	}}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	identities := map[string][]RelationshipTarget{}
	addresses := map[string][]RelationshipTarget{}
	memberTargets := map[string][]RelationshipTarget{}
	target := func(id string, ref EvidenceResource) RelationshipTarget {
		cluster := clusters[id]
		return RelationshipTarget{ClusterID: id, Resource: ref, ObservedAt: cluster.ObservedAt, State: contextForCluster(cluster, now).State}
	}
	for _, id := range ids {
		cluster, exists := clusters[id]
		state := "unavailable"
		if exists {
			state = contextForCluster(cluster, now).State
		}
		if exists && cluster.RelationshipFacts == nil {
			state = "not_collected"
		}
		result.Coverage = append(result.Coverage, RelationshipCoverage{ClusterID: id, State: state, Facts: len(cluster.RelationshipFacts)})
		for _, fact := range cluster.RelationshipFacts {
			if fact.Type == "cluster_identity" && fact.Value != "" {
				identities[fact.Value] = append(identities[fact.Value], target(id, fact.Resource))
			}
			if fact.Type == "address" {
				addresses[fact.Value] = append(addresses[fact.Value], target(id, fact.Resource))
			}
		}
	}
	appendRelation := func(rel Relationship) bool {
		if len(result.Relationships) == 200 {
			result.Truncated = true
			return false
		}
		if len(rel.Targets) > 100 {
			rel.Targets = slices.Clone(rel.Targets[:100])
			result.Truncated = true
		}
		if rel.State == "declared" {
			result.State = "declared_membership_observed"
		} else if result.State == "unknown" && len(rel.Targets) > 0 {
			result.State = "candidates_only"
		}
		result.Relationships = append(result.Relationships, rel)
		return true
	}
	for _, id := range ids {
		for _, fact := range clusters[id].RelationshipFacts {
			if fact.Type != "federation_member" {
				continue
			}
			candidates := slices.Clone(identities[fact.Value])
			if candidates == nil {
				candidates = []RelationshipTarget{}
			}
			rel := Relationship{Type: "federation_membership", State: "unresolved", Source: target(id, fact.Resource), Targets: candidates, Evidence: fact}
			if len(candidates) > 1 {
				rel.State = "ambiguous"
			}
			if len(candidates) == 1 && candidates[0].ClusterID != id {
				rel.State = "declared"
				if candidates[0].State != "current" || rel.Source.State != "current" {
					rel.State = "stale_evidence"
				}
			}
			memberTargets[id+"\x00"+fact.MemberName] = candidates
			if !appendRelation(rel) {
				return result
			}
		}
	}
	for _, id := range ids {
		for _, fact := range clusters[id].RelationshipFacts {
			switch fact.Type {
			case "ip_reference":
				candidates := slices.Clone(addresses[fact.Value])
				if !slices.ContainsFunc(candidates, func(item RelationshipTarget) bool { return item.ClusterID != id }) {
					continue
				}
				state := "candidate"
				if len(candidates) != 1 {
					state = "ambiguous"
				}
				rel := Relationship{Type: "ip_reference", State: state, Source: target(id, fact.Resource), Targets: candidates, Evidence: fact}
				if !appendRelation(rel) {
					return result
				}
			case "distribution":
				candidates := slices.Clone(memberTargets[id+"\x00"+fact.MemberName])
				if candidates == nil {
					candidates = []RelationshipTarget{}
				}
				rel := Relationship{Type: "declared_distribution", State: "unresolved", Source: target(id, fact.Resource), Targets: candidates, Evidence: fact}
				if len(candidates) > 1 {
					rel.State = "ambiguous"
				}
				if len(candidates) == 1 && candidates[0].ClusterID != id {
					rel.State = "declared"
					if candidates[0].State != "current" || rel.Source.State != "current" {
						rel.State = "stale_evidence"
					}
				}
				if !appendRelation(rel) {
					return result
				}
			}
		}
	}
	return result
}

func validRelationshipFact(fact RelationshipFact) bool {
	if fact.Type == "address" || fact.Type == "ip_reference" {
		ip, err := netip.ParseAddr(fact.Value)
		if err != nil || ip.Unmap().String() != fact.Value {
			return false
		}
	}
	return slices.Contains([]string{"cluster_identity", "address", "ip_reference", "federation_member", "distribution"}, fact.Type) &&
		fact.Resource.Kind != "" && fact.Resource.Name != "" && strings.TrimSpace(fact.Value) != "" && len(fact.Value) <= 1024
}
