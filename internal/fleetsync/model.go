package fleetsync

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"
)

const (
	Version          = 1
	MaxSnapshotBytes = 32 << 20
	MaxDocuments     = 10000
	StaleAfter       = 3 * time.Minute
)

var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type Document struct {
	Tool  string          `json:"tool"`
	Input json.RawMessage `json:"input"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

type Snapshot struct {
	Version     int        `json:"version"`
	ClusterID   string     `json:"clusterId"`
	Name        string     `json:"name"`
	SourceID    string     `json:"sourceId"`
	ObservedAt  time.Time  `json:"observedAt"`
	Documents   []Document `json:"documents"`
	Limitations []string   `json:"limitations"`
}

type Cluster struct {
	Snapshot
	ReceivedAt time.Time `json:"receivedAt"`
}

type Group struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ClusterIDs []string `json:"clusterIds"`
}

type Selection struct {
	ClusterID string `json:"clusterId,omitempty"`
	FleetID   string `json:"fleetId,omitempty"`
}

type ClusterContext struct {
	ClusterID   string     `json:"clusterId"`
	Name        string     `json:"name"`
	SourceID    string     `json:"sourceId,omitempty"`
	ObservedAt  time.Time  `json:"observedAt"`
	ReceivedAt  time.Time  `json:"receivedAt"`
	State       string     `json:"state"`
	Documents   []Document `json:"documents"`
	Limitations []string   `json:"limitations"`
}

type AgentContext struct {
	Mode        string           `json:"mode"`
	Fleet       *Group           `json:"fleet,omitempty"`
	Clusters    []ClusterContext `json:"clusters"`
	Limitations []string         `json:"limitations"`
}

func ValidateID(id string) bool { return validID.MatchString(id) }

func (s Snapshot) Validate(now time.Time) error {
	if s.Version != Version || !ValidateID(s.ClusterID) || s.Name == "" || len(s.Name) > 256 || s.SourceID == "" || len(s.SourceID) > 256 {
		return errors.New("invalid snapshot identity or version")
	}
	if s.ObservedAt.IsZero() || s.ObservedAt.After(now.Add(time.Minute)) || len(s.Documents) > MaxDocuments {
		return errors.New("invalid snapshot time or document count")
	}
	keys := make(map[string]bool)
	for _, d := range s.Documents {
		if !slices.Contains([]string{"get_dashboard", "get_topology", "get_events", "list_resources", "get_resource"}, d.Tool) || !json.Valid(d.Input) {
			return errors.New("invalid snapshot document")
		}
		var input map[string]any
		if json.Unmarshal(d.Input, &input) != nil || input == nil {
			return errors.New("document input must be an object")
		}
		if (d.Error == "") == (len(d.Data) == 0) || (len(d.Data) > 0 && !json.Valid(d.Data)) {
			return errors.New("document requires either data or an error")
		}
		canonical, _ := json.Marshal(input)
		key := d.Tool + ":" + string(canonical)
		if keys[key] {
			return errors.New("duplicate snapshot document")
		}
		keys[key] = true
	}
	return nil
}

// AssembleContext keeps each canonical KubeAtlas payload inside its source cluster.
// Fleet membership is administrative grouping, not evidence of a resource relationship.
func AssembleContext(selection Selection, clusters map[string]Cluster, groups map[string]Group, now time.Time) (AgentContext, error) {
	result := AgentContext{Mode: "single", Clusters: []ClusterContext{}, Limitations: []string{
		"Synchronized observations, not live Kubernetes reads. Inspect per-document coverage and per-cluster freshness.",
		"Resource details are retrieved separately with get_resource_context; no live logs or write operations are available.",
	}}
	if (selection.ClusterID == "") == (selection.FleetID == "") {
		return result, errors.New("select exactly one clusterId or fleetId")
	}
	ids := []string{selection.ClusterID}
	if selection.FleetID != "" {
		group, ok := groups[selection.FleetID]
		if !ok {
			return result, errors.New("unknown fleetId")
		}
		group.ClusterIDs = slices.Clone(group.ClusterIDs)
		result.Mode, result.Fleet = "multi_cloud", &group
		ids = group.ClusterIDs
		result.Limitations = append(result.Limitations, "Membership does not establish connectivity, failover, or application identity across clusters. Resource and topology IDs are local to each clusterId.")
	} else if _, ok := clusters[selection.ClusterID]; !ok {
		return result, errors.New("unknown clusterId")
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	for _, id := range ids {
		cluster, ok := clusters[id]
		if !ok {
			result.Clusters = append(result.Clusters, ClusterContext{ClusterID: id, State: "unavailable", Documents: []Document{}, Limitations: []string{"No synchronized snapshot is available."}})
			continue
		}
		view := contextForCluster(cluster, now)
		for _, document := range cluster.Documents {
			if document.Tool != "get_resource" {
				view.Documents = append(view.Documents, cloneDocument(document))
			}
		}
		result.Clusters = append(result.Clusters, view)
	}
	return result, nil
}

type ResourceSelection struct {
	ClusterID string `json:"clusterId"`
	Kind      string `json:"kind"`
	Group     string `json:"group,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

func ResourceContext(selection ResourceSelection, cluster Cluster, now time.Time) (ClusterContext, error) {
	result := contextForCluster(cluster, now)
	if selection.ClusterID != cluster.ClusterID || selection.Kind == "" || selection.Name == "" {
		return result, errors.New("explicit clusterId, kind and name are required")
	}
	for _, document := range cluster.Documents {
		if document.Tool != "get_resource" {
			continue
		}
		var input ResourceSelection
		if json.Unmarshal(document.Input, &input) != nil {
			continue
		}
		if input.Kind == selection.Kind && input.Group == selection.Group && input.Namespace == selection.Namespace && input.Name == selection.Name {
			result.Documents = append(result.Documents, cloneDocument(document))
			return result, nil
		}
	}
	return result, errors.New("resource detail not synchronized; consult inventory coverage and snapshot limitations")
}

func contextForCluster(cluster Cluster, now time.Time) ClusterContext {
	state := "current"
	if now.Sub(cluster.ObservedAt) > StaleAfter || now.Sub(cluster.ReceivedAt) > StaleAfter {
		state = "stale"
	}
	return ClusterContext{ClusterID: cluster.ClusterID, Name: cluster.Name, SourceID: cluster.SourceID,
		ObservedAt: cluster.ObservedAt, ReceivedAt: cluster.ReceivedAt, State: state,
		Documents: []Document{}, Limitations: slices.Clone(cluster.Limitations)}
}

func cloneDocument(d Document) Document {
	d.Input = slices.Clone(d.Input)
	d.Data = slices.Clone(d.Data)
	return d
}
