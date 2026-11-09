package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type inputConstraint struct {
	enum []any
	min  *float64
	max  *float64
}

func inputEnum(values ...string) inputConstraint {
	constraint := inputConstraint{enum: make([]any, len(values))}
	for i, value := range values {
		constraint.enum[i] = value
	}
	return constraint
}

func inputRange(minimum, maximum float64) inputConstraint {
	return inputConstraint{min: &minimum, max: &maximum}
}

var toolInputConstraints = map[string]map[string]inputConstraint{
	"manage_workload": {
		"action":   inputEnum("restart", "scale", "rollback"),
		"replicas": {min: new(float64(0))},
	},
	"manage_rollout": {"action": inputEnum("abort", "retry", "promote", "promote-full", "skip-step")},
	"manage_cronjob": {"action": inputEnum("trigger", "suspend", "resume")},
	"manage_gitops":  {"action": inputEnum("sync", "refresh", "terminate", "rollback", "reconcile", "sync-with-source", "suspend", "resume")},
	"manage_node": {
		"action":  inputEnum("cordon", "uncordon", "drain"),
		"timeout": inputRange(1, 600),
	},
	"apply_resource":       {"mode": inputEnum("apply", "create")},
	"patch_resource":       {"patch_type": inputEnum("json", "merge", "strategic")},
	"exec_pod":             {"timeout_seconds": inputRange(1, float64(execMaxTimeout/time.Second))},
	"wait_for":             {"timeout_seconds": inputRange(1, float64(waitMaxTimeout/time.Second))},
	"query_prometheus":     {"timeout": inputRange(1, float64(promMaxQueryTimeout/time.Second))},
	"top_resources":        {"limit": inputRange(1, 100)},
	"get_events":           {"limit": inputRange(1, 100)},
	"get_changes":          {"limit": inputRange(1, 50)},
	"search":               {"limit": inputRange(1, 500)},
	"issues":               {"limit": inputRange(1, 1000)},
	"get_cluster_audit":    {"limit": inputRange(1, 100)},
	"get_runtime_versions": {"limit": inputRange(1, 200)},
	"discover_metrics":     {"limit": inputRange(1, promDiscoverMaxLimit)},
	"get_prometheus_rules": {"limit": inputRange(1, promRulesMaxLimit)},
	"get_cost":             {"limit": inputRange(1, costMaxLimit)},
	"get_rightsizing":      {"limit": inputRange(1, rightsizingMaxLimit)},
}

// jsonschema struct tags carry descriptions only. Constrain the same schema
// that the SDK publishes and validates, rather than just decorating tools/list.
func constrainToolInput[In any](tool *mcpsdk.Tool) *mcpsdk.Tool {
	constraints := toolInputConstraints[tool.Name]
	if len(constraints) == 0 {
		return tool
	}
	var schema *jsonschema.Schema
	var err error
	if tool.InputSchema == nil {
		typ := reflect.TypeFor[In]()
		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		schema, err = jsonschema.ForType(typ, nil)
	} else {
		var data []byte
		data, err = json.Marshal(tool.InputSchema)
		if err == nil {
			err = json.Unmarshal(data, &schema)
		}
	}
	if err != nil || schema == nil {
		panic(fmt.Sprintf("MCP input schema for %s: %v", tool.Name, err))
	}
	for name, constraint := range constraints {
		property := schema.Properties[name]
		if property == nil {
			panic(fmt.Sprintf("MCP input constraint names absent parameter %s.%s", tool.Name, name))
		}
		if constraint.enum != nil {
			property.Enum = append([]any(nil), constraint.enum...)
		}
		if constraint.min != nil {
			property.Minimum = new(*constraint.min)
		}
		if constraint.max != nil {
			property.Maximum = new(*constraint.max)
		}
	}
	// Do not inject defaults: some limits apply only to particular views, and
	// SDK defaults would turn an omitted limit into a prohibited explicit one.
	copy := *tool
	copy.InputSchema = schema
	return &copy
}
