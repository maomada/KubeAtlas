package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/maomada/KubeAtlas/internal/fleetsync"
	"github.com/maomada/KubeAtlas/internal/k8s"
	"github.com/maomada/KubeAtlas/pkg/resourceid"
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
	digest := sha256.Sum256([]byte(binding))
	snapshot := fleetsync.Snapshot{
		Version: fleetsync.Version, ClusterID: clusterID, Name: name, SourceID: hex.EncodeToString(digest[:]),
		ObservedAt: time.Now().UTC(), Documents: []fleetsync.Document{},
		Limitations: []string{"Periodic full snapshots; collection is not an atomic Kubernetes transaction.", "Secret objects and live logs are not exported. Other payloads retain the original KubeAtlas MCP projections and may contain operationally sensitive information."},
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

	type kind struct{ group, resource string }
	kinds := map[kind]bool{}
	for _, builtin := range resourceid.Builtins {
		kinds[kind{builtin.Group, builtin.Resource}] = true
	}
	if discovery := k8s.GetResourceDiscovery(); discovery != nil {
		catalog := discovery.Catalog()
		if !catalog.Complete {
			snapshot.Limitations = append(snapshot.Limitations, "API discovery is incomplete; undiscovered resource kinds may be missing.")
		}
		for _, resource := range catalog.Resources {
			if !strings.Contains(resource.Name, "/") && slices.Contains(resource.Verbs, "list") && slices.Contains(resource.Verbs, "get") {
				kinds[kind{resource.Group, resource.Name}] = true
			}
		}
	} else {
		snapshot.Limitations = append(snapshot.Limitations, "API discovery unavailable; only built-in resource kinds were attempted.")
	}
	ordered := make([]kind, 0, len(kinds))
	for item := range kinds {
		if item.group == "" && item.resource == "secrets" {
			continue
		}
		ordered = append(ordered, item)
	}
	slices.SortFunc(ordered, func(a, b kind) int { return strings.Compare(a.group+"/"+a.resource, b.group+"/"+b.resource) })
	details := 0
	for _, item := range ordered {
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
		}) int { return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name) })
		for _, resource := range list.Items {
			if resource.Name == "" {
				continue
			}
			if details >= 5000 {
				snapshot.Limitations = appendOnce(snapshot.Limitations, "Resource detail limit reached (5000); inventories retain their original coverage but some details are not synchronized.")
				break
			}
			input := getResourceInput{Kind: item.resource, Group: item.group, Namespace: resource.Namespace, Name: resource.Name}
			add("get_resource", input, func() (*mcpsdk.CallToolResult, any, error) { return handleGetResource(ctx, nil, input) })
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
