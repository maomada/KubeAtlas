package mcp

import (
	"context"
	"time"

	"github.com/maomada/KubeAtlas/internal/opencost"
	"github.com/maomada/KubeAtlas/internal/prometheus"
	"github.com/maomada/KubeAtlas/pkg/prom"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type CapabilityDependency struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type SourceCapabilities struct {
	ObservedAt        time.Time                       `json:"observedAt"`
	MCPEnabled        *bool                           `json:"mcpEnabled,omitempty"`
	ReadOnly          *bool                           `json:"readOnly,omitempty"`
	ExecEnabled       *bool                           `json:"execEnabled,omitempty"`
	AllowedOperations []string                        `json:"allowedOperations"`
	Dependencies      map[string]CapabilityDependency `json:"dependencies"`
	Limitations       []string                        `json:"limitations"`
}

type sourceCapabilitiesResponse struct {
	Capabilities SourceCapabilities           `json:"capabilities"`
	Pagination   sourcePaginationCapabilities `json:"pagination"`
}

type sourcePaginationCapabilities struct {
	MaxLimit              int    `json:"maxLimit"`
	CursorLifetimeSeconds int    `json:"cursorLifetimeSeconds"`
	Consistency           string `json:"consistency"`
}

func sourceCapabilitiesHandler(includeWrites, registeredExec bool) func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
	return func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		var status *prom.Status
		if client := prometheus.GetClient(); client != nil {
			cached := client.GetStatus()
			status = &cached
		}
		dependencies := sourceDependencies(status, opencost.StatusSnapshot())
		enabled, readOnly, exec := true, !includeWrites, registeredExec
		operations := []string{"read"}
		if includeWrites {
			operations = append(operations, "write")
		}
		if registeredExec {
			operations = append(operations, "exec")
		}
		return toJSONResult(sourceCapabilitiesResponse{
			Capabilities: SourceCapabilities{
				ObservedAt: time.Now().UTC(), MCPEnabled: &enabled, ReadOnly: &readOnly, ExecEnabled: &exec,
				AllowedOperations: operations, Dependencies: dependencies,
				Limitations: []string{
					"Exposed operation classes are not authorization grants: every read/write/exec still requires per-request Kubernetes RBAC. Namespace permissions were not evaluated.",
					"Cached metadata only, not a health check or an active dependency/data probe.",
					"Describes this source runtime, not a cluster-global inventory.",
				},
			},
			Pagination: sourcePaginationCapabilities{MaxLimit: resourcePageMaxLimit, CursorLifetimeSeconds: int(resourceCursorLifetime / time.Second), Consistency: "live_non_atomic"},
		})
	}
}

func sourceDependencies(status *prom.Status, cost opencost.Status) map[string]CapabilityDependency {
	metrics := CapabilityDependency{State: "unknown", Reason: "No cached connection; not proof Prometheus is absent. No data probe was performed."}
	if status != nil {
		if status.Connected {
			metrics = CapabilityDependency{State: "connected", Reason: "Cached Prometheus connection only; no data probe was performed."}
		} else if status.Discovering {
			metrics = CapabilityDependency{State: "discovering", Reason: "Existing Prometheus discovery is in progress; this tool did not start it or probe data."}
		}
	}
	costReason := cost.Reason
	if cost.Source != "" {
		costReason += "; source=" + string(cost.Source)
	}
	return map[string]CapabilityDependency{
		"prometheus":      metrics,
		"costs":           {State: cost.State, Reason: costReason},
		"logs":            {State: "conditional", Reason: "Requires a live Pod and per-request Kubernetes pods/log RBAC; neither was probed."},
		"imageMetadata":   {State: "conditional", Reason: "Requires registry reachability and identity-permitted credentials; no network or permission probe was performed."},
		"runtimeVersions": {State: "conditional", Reason: runtimeVersionsBasis},
	}
}
