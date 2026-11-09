package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/maomada/KubeAtlas/internal/fleetsync"
	"github.com/maomada/KubeAtlas/internal/k8s"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CaptureFleetSnapshot uses the same read projections as the single-cluster MCP.
// A context switch invalidates the entire capture, including switches away and back.
func CaptureFleetSnapshot(ctx context.Context, clusterID, name, binding string) (fleetsync.Snapshot, error) {
	operation := k8s.OperationContext()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(operation, cancel)
	defer stop()
	defer cancel()
	cache := k8s.GetResourceCache()
	if cache == nil || binding == "" || k8s.ClusterSafetyBinding(ctx) != binding {
		return fleetsync.Snapshot{}, errors.New("Fleet sync requires the configured cluster connection")
	}
	snapshot := fleetsync.Snapshot{
		Version: fleetsync.Version, ClusterID: clusterID, Name: name, SourceID: fleetsync.SourceIDForBinding(binding),
		ObservedAt: time.Now().UTC(), Documents: []fleetsync.Document{},
		Limitations:       []string{"Periodic full snapshots; collection is not an atomic Kubernetes transaction.", "Secret objects and live logs are not exported. Other payloads retain the original KubeAtlas MCP projections and may contain operationally sensitive information."},
		RelationshipFacts: []fleetsync.RelationshipFact{},
	}
	bytesUsed := 0
	add := func(tool string, input any, call func() (*mcpsdk.CallToolResult, any, error)) json.RawMessage {
		if ctx.Err() != nil {
			return nil
		}
		if len(snapshot.Documents) >= fleetsync.MaxDocuments {
			snapshot.Limitations = appendOnce(snapshot.Limitations, "Document limit reached; snapshot is incomplete.")
			return nil
		}
		args, _ := json.Marshal(input)
		document := fleetsync.Document{Tool: tool, Input: args}
		result, _, err := call()
		if err != nil {
			document.Error = err.Error()
		} else if result == nil || result.IsError || len(result.Content) != 1 {
			document.Error = "canonical MCP read did not produce a successful JSON result"
		} else if content, ok := result.Content[0].(*mcpsdk.TextContent); ok && json.Valid([]byte(content.Text)) {
			document.Data = json.RawMessage(content.Text)
		} else {
			document.Error = "canonical MCP read did not produce JSON"
		}
		encoded, _ := json.Marshal(document)
		if bytesUsed+len(encoded) > fleetsync.MaxSnapshotBytes-(1<<20) {
			document.Data = nil
			document.Error = "document omitted: snapshot byte limit reached"
			snapshot.Limitations = appendOnce(snapshot.Limitations, "Snapshot byte limit reached; some documents are omitted.")
			encoded, _ = json.Marshal(document)
		}
		bytesUsed += len(encoded)
		snapshot.Documents = append(snapshot.Documents, document)
		return document.Data
	}
	add("get_dashboard", dashboardInput{}, func() (*mcpsdk.CallToolResult, any, error) { return handleGetDashboard(ctx, nil, dashboardInput{}) })
	for _, view := range []string{"resources", "traffic"} {
		input := topologyInput{View: view, Format: "graph"}
		add("get_topology", input, func() (*mcpsdk.CallToolResult, any, error) { return handleGetTopology(ctx, nil, input) })
	}
	add("get_events", eventsInput{Limit: 100}, func() (*mcpsdk.CallToolResult, any, error) { return handleGetEvents(ctx, nil, eventsInput{Limit: 100}) })

	plan := currentCollectionPlan()
	snapshot.Coverage = &plan.coverage
	for _, limitation := range plan.coverage.Limitations {
		snapshot.Limitations = appendOnce(snapshot.Limitations, limitation)
	}
	details := 0
	for _, item := range plan.kinds {
		if ctx.Err() != nil {
			break
		}
		input := listResourcesInput{Kind: item.resource, Group: item.group}
		data := add("list_resources", input, func() (*mcpsdk.CallToolResult, any, error) { return handleListResources(ctx, nil, input) })
		if len(data) == 0 {
			continue
		}
		var list struct {
			Items []struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"items"`
		}
		if err := json.Unmarshal(data, &list); err != nil {
			return fleetsync.Snapshot{}, fmt.Errorf("decode canonical resource inventory: %w", err)
		}
		slices.SortFunc(list.Items, func(a, b struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		}) int {
			return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
		})
		for _, resource := range list.Items {
			if resource.Name == "" {
				continue
			}
			if details >= 5000 {
				snapshot.Limitations = appendOnce(snapshot.Limitations, "Resource detail limit reached (5000); inventories retain their original coverage but some details are not synchronized.")
				break
			}
			input := getResourceInput{Kind: item.resource, Group: item.group, Namespace: resource.Namespace, Name: resource.Name}
			data := add("get_resource", input, func() (*mcpsdk.CallToolResult, any, error) { return handleGetResource(ctx, nil, input) })
			ref := fleetsync.EvidenceResource{Group: item.group, Kind: item.resource, Namespace: resource.Namespace, Name: resource.Name}
			if len(data) > 0 {
				if item.group == "" && slices.Contains([]string{"namespaces", "nodes", "pods", "services", "endpoints"}, item.resource) {
					if raw, err := k8s.FetchResource(cache, item.resource, resource.Namespace, resource.Name); err == nil {
						data, _ = json.Marshal(raw)
					} else {
						snapshot.Limitations = appendOnce(snapshot.Limitations, "Some original network/identity facts were unavailable; canonical MCP documents omit some IPs and UIDs.")
					}
				}
				facts := fleetsync.ExtractRelationshipFacts(ref, data)
				if len(facts) == 512 {
					snapshot.Limitations = appendOnce(snapshot.Limitations, "A resource reached the 512 relationship-fact limit; additional facts may be omitted.")
				}
				encoded, _ := json.Marshal(facts)
				if len(snapshot.RelationshipFacts)+len(facts) <= 20000 && bytesUsed+len(encoded) < fleetsync.MaxSnapshotBytes-(1<<20) {
					snapshot.RelationshipFacts = append(snapshot.RelationshipFacts, facts...)
					bytesUsed += len(encoded)
				} else {
					snapshot.Limitations = appendOnce(snapshot.Limitations, "Relationship facts truncated by snapshot size/count limits.")
				}
			}
			details++
		}
	}
	if ctx.Err() != nil || operation.Err() != nil || k8s.GetResourceCache() != cache || k8s.ClusterSafetyBinding(ctx) != binding {
		return fleetsync.Snapshot{}, errors.New("cluster connection changed or snapshot collection was canceled")
	}
	if err := snapshot.Validate(time.Now()); err != nil {
		return fleetsync.Snapshot{}, err
	}
	return snapshot, nil
}

func appendOnce(values []string, value string) []string {
	if !slices.Contains(values, value) {
		return append(values, value)
	}
	return values
}
