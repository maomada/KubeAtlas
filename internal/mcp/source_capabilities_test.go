package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/k8s"
	"github.com/maomada/KubeAtlas/internal/opencost"
	"github.com/maomada/KubeAtlas/pkg/prom"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSourceCapabilitiesRegisteredModes(t *testing.T) {
	previous := execToolsEnabled.Load()
	t.Cleanup(func() { SetExecToolsEnabled(previous) })
	for _, tc := range []struct {
		name                  string
		writes, exec, enabled bool
	}{
		{"readonly", false, false, false},
		{"readonly ignores exec", false, true, true},
		{"full", true, true, false},
		{"exec disabled by server", true, false, true},
		{"full exec", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetExecToolsEnabled(tc.enabled)
			server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "capabilities-test", Version: "test"}, nil)
			registerTools(server, tc.writes, tc.exec, newToolParamRegistry())
			SetExecToolsEnabled(!tc.enabled)
			client := connectTo(t, server)
			catalog, err := client.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			var capabilityTool *mcpsdk.Tool
			var execRegistered bool
			for _, tool := range catalog.Tools {
				if tool.Name == "exec_pod" {
					execRegistered = true
				}
				if tool.Name == "get_capabilities" {
					capabilityTool = tool
				}
			}
			if capabilityTool == nil || capabilityTool.Annotations == nil || !capabilityTool.Annotations.ReadOnlyHint {
				t.Fatal("missing read-only get_capabilities")
			}
			rawSchema, _ := json.Marshal(capabilityTool.InputSchema)
			var schema struct {
				Type       string         `json:"type"`
				Properties map[string]any `json:"properties"`
			}
			if json.Unmarshal(rawSchema, &schema) != nil || schema.Type != "object" || len(schema.Properties) != 0 {
				t.Fatalf("input is not an empty object: %s", rawSchema)
			}
			result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "get_capabilities", Arguments: map[string]any{}})
			if err != nil || result.IsError {
				t.Fatalf("capabilities call failed: %v %+v", err, result)
			}
			var body sourceCapabilitiesResponse
			if err := json.Unmarshal([]byte(extractText(t, result)), &body); err != nil {
				t.Fatal(err)
			}
			caps := body.Capabilities
			if caps.MCPEnabled == nil || !*caps.MCPEnabled || caps.ReadOnly == nil || *caps.ReadOnly != !tc.writes || caps.ExecEnabled == nil || *caps.ExecEnabled != execRegistered {
				t.Fatalf("runtime flags disagree with registration: %+v", caps)
			}
			want := []string{"read"}
			if tc.writes {
				want = append(want, "write")
			}
			if execRegistered {
				want = append(want, "exec")
			}
			if !slices.Equal(caps.AllowedOperations, want) {
				t.Fatalf("operations = %v, want %v", caps.AllowedOperations, want)
			}
			if caps.ObservedAt.IsZero() || body.Pagination != (sourcePaginationCapabilities{200, 600, "live_non_atomic"}) {
				t.Fatal("missing observation/pagination contract")
			}
			if _, err := time.Parse(time.RFC3339Nano, result.Meta["kubeatlas/observedAt"].(string)); err != nil {
				t.Fatal("wrapper observation missing", err)
			}
			limitations := strings.Join(caps.Limitations, " ")
			for _, term := range []string{"per-request Kubernetes RBAC", "Namespace permissions were not evaluated", "not a health check", "not a cluster-global inventory"} {
				if !strings.Contains(limitations, term) {
					t.Fatalf("missing limitation: %s", term)
				}
			}
		})
	}
}

func TestSourceCapabilitiesDependenciesAreCachedNotProof(t *testing.T) {
	for _, tc := range []struct {
		status *prom.Status
		want   string
	}{
		{nil, "unknown"},
		{&prom.Status{Error: "private-error", Address: "private-address", ContextName: "private-context"}, "unknown"},
		{&prom.Status{Connected: true, Address: "private-address"}, "connected"},
		{&prom.Status{Discovering: true}, "discovering"},
	} {
		deps := sourceDependencies(tc.status, opencost.Status{State: "unknown", Reason: "not_probed", Source: opencost.SourceAuto})
		if deps["prometheus"].State != tc.want || deps["costs"].State != "unknown" {
			t.Fatalf("incorrect dependency status: %+v", deps)
		}
		if deps["runtimeVersions"].Reason != runtimeVersionsBasis {
			t.Fatal("runtime declaration evidence drifted")
		}
		for _, dependency := range []string{"logs", "imageMetadata", "runtimeVersions"} {
			if deps[dependency].State != "conditional" {
				t.Fatalf("dependency reported as guaranteed: %s", dependency)
			}
		}
		raw, _ := json.Marshal(deps)
		if strings.Contains(string(raw), "private-") {
			t.Fatal("cached status leaked address/error/identity")
		}
	}
	for _, status := range []opencost.Status{
		{State: "unavailable", Reason: "no_cost_source", Source: opencost.SourceAuto},
		{State: "unavailable", Reason: "cost_source_unavailable", Source: opencost.SourceKubecost},
		{State: "unavailable", Reason: "invalid_configuration", Source: opencost.SourceAuto},
		{State: "selected", Reason: "selection_not_data_availability", Source: opencost.SourcePrometheus},
	} {
		got := sourceDependencies(nil, status)["costs"]
		if got.State != status.State || !strings.Contains(got.Reason, status.Reason) || !strings.Contains(got.Reason, string(status.Source)) {
			t.Fatalf("cached cost state lost: %+v", got)
		}
	}
}

func TestSourceCapabilitiesDoNotRefreshDiscovery(t *testing.T) {
	fixture := &collectionDiscoveryFixture{}
	restore, err := k8s.SetTestResourceDiscovery(fixture)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	before := fixture.calls.Load()
	plan := currentCollectionPlan()
	result, _, err := sourceCapabilitiesHandler(false, false)(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	var body sourceCapabilitiesResponse
	if err := json.Unmarshal([]byte(extractText(t, result)), &body); err != nil {
		t.Fatal(err)
	}
	if fixture.calls.Load() != before {
		t.Fatal("capabilities triggered discovery")
	}
	if !reflect.DeepEqual(body.Collection, plan.coverage) {
		t.Fatal("capability coverage drifted from snapshot policy")
	}
}
