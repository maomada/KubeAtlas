package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolInputConstraintsPublished(t *testing.T) {
	enableExecToolsForTest(t)
	schemas := map[string]*jsonschema.Schema{}
	for _, tool := range listRegisteredTools(t) {
		data, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		schemas[tool.Name] = &schema
	}
	property := func(tool, name string) *jsonschema.Schema {
		t.Helper()
		if schemas[tool] == nil || schemas[tool].Properties[name] == nil {
			t.Fatalf("missing published parameter %s.%s", tool, name)
		}
		return schemas[tool].Properties[name]
	}
	for _, tc := range []struct {
		tool, name string
		values     []any
	}{
		{"manage_workload", "action", []any{"restart", "scale", "rollback"}},
		{"manage_rollout", "action", []any{"abort", "retry", "promote", "promote-full", "skip-step"}},
		{"manage_cronjob", "action", []any{"trigger", "suspend", "resume"}},
		{"manage_gitops", "action", []any{"sync", "refresh", "terminate", "rollback", "reconcile", "sync-with-source", "suspend", "resume"}},
		{"manage_node", "action", []any{"cordon", "uncordon", "drain"}},
		{"apply_resource", "mode", []any{"apply", "create"}},
		{"patch_resource", "patch_type", []any{"json", "merge", "strategic"}},
	} {
		if got := property(tc.tool, tc.name).Enum; !reflect.DeepEqual(got, tc.values) {
			t.Errorf("%s.%s enum = %v, want %v", tc.tool, tc.name, got, tc.values)
		}
	}
	if got := property("manage_workload", "replicas").Minimum; got == nil || *got != 0 {
		t.Fatal("replicas must publish minimum 0")
	}
	for _, tc := range []struct {
		tool, name string
		maximum    float64
	}{
		{"manage_node", "timeout", 600},
		{"exec_pod", "timeout_seconds", 45},
		{"wait_for", "timeout_seconds", 45},
		{"query_prometheus", "timeout", 180},
		{"top_resources", "limit", 100},
		{"get_events", "limit", 100},
		{"get_changes", "limit", 50},
		{"search", "limit", 500},
		{"issues", "limit", 1000},
		{"get_cluster_audit", "limit", 100},
		{"get_runtime_versions", "limit", 200},
		{"discover_metrics", "limit", 500},
		{"get_prometheus_rules", "limit", 200},
		{"get_cost", "limit", 100},
		{"get_rightsizing", "limit", 100},
	} {
		p := property(tc.tool, tc.name)
		if p.Minimum == nil || *p.Minimum != 1 || p.Maximum == nil || *p.Maximum != tc.maximum {
			t.Errorf("%s.%s must publish range 1..%v: %+v", tc.tool, tc.name, tc.maximum, p)
		}
		resolved, err := p.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []float64{-1, 0, tc.maximum + 1} {
			if err := resolved.Validate(value); err == nil {
				t.Errorf("%s.%s accepted %v", tc.tool, tc.name, value)
			}
		}
		for _, value := range []float64{1, tc.maximum} {
			if err := resolved.Validate(value); err != nil {
				t.Errorf("%s.%s rejected boundary %v: %v", tc.tool, tc.name, value, err)
			}
		}
	}
}

func TestToolInputConstraintsRejectBeforeHandlerWithCorrectionHelp(t *testing.T) {
	client := connectTestServer(t)
	for _, tc := range []struct {
		tool, arguments string
	}{
		{"manage_workload", `{"action":"bad","kind":"deployment","namespace":"app","name":"api"}`},
		{"manage_workload", `{"action":"scale","kind":"deployment","namespace":"app","name":"api","replicas":-1}`},
		{"manage_node", `{"action":"drain","name":"node","timeout":601}`},
		{"wait_for", `{"kind":"pod","namespace":"app","name":"api","timeout_seconds":0}`},
		{"get_events", `{"limit":101}`},
		{"patch_resource", `{"kind":"Deployment","name":"api","patch":"{}","patch_type":"invalid"}`},
	} {
		t.Run(tc.tool+tc.arguments, func(t *testing.T) {
			result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: tc.tool, Arguments: json.RawMessage(tc.arguments)})
			if err != nil {
				t.Fatal(err)
			}
			text := extractText(t, result)
			if !result.IsError || !strings.Contains(text, `validating "arguments"`) || !strings.Contains(text, "accepts:") {
				t.Fatalf("expected schema rejection with existing correction help, got %s", text)
			}
		})
	}
}

func TestToolInputConstraintsPreserveZeroReplicasAndOmittedLimits(t *testing.T) {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "constraints", Version: "test"}, nil)
	registry := newToolParamRegistry()
	var scaled bool
	addToolWithRegistry(registry, server, &mcpsdk.Tool{Name: "manage_workload"}, func(_ context.Context, _ *mcpsdk.CallToolRequest, input manageWorkloadInput) (*mcpsdk.CallToolResult, any, error) {
		scaled = input.Replicas != nil && *input.Replicas == 0
		return toJSONResult(map[string]bool{"scaled": scaled})
	})
	var omitted bool
	addToolWithRegistry(registry, server, &mcpsdk.Tool{Name: "get_cost"}, func(_ context.Context, _ *mcpsdk.CallToolRequest, input getCostInput) (*mcpsdk.CallToolResult, any, error) {
		omitted = input.View == "trend" && input.Limit == 0
		return toJSONResult(map[string]bool{"omitted": omitted})
	})
	client := connectTo(t, server)
	for _, tc := range []struct {
		tool, arguments string
	}{
		{"manage_workload", `{"action":"scale","kind":"deployment","namespace":"app","name":"api","replicas":0}`},
		{"get_cost", `{"view":"trend"}`},
	} {
		result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: tc.tool, Arguments: json.RawMessage(tc.arguments)})
		if err != nil || result.IsError {
			t.Fatalf("%s: result=%+v err=%v", tc.tool, result, err)
		}
	}
	if !scaled || !omitted {
		t.Fatalf("valid inputs changed: scaled=%v omitted=%v", scaled, omitted)
	}
}
