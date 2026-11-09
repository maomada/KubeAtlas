package fleetsync

import (
	"maps"
	"slices"
	"time"
)

type UnsupportedKind struct {
	Group    string `json:"group"`
	Version  string `json:"version"`
	Resource string `json:"resource"`
	Kind     string `json:"kind"`
	Reason   string `json:"reason"`
}

type CollectionCoverage struct {
	DiscoveryComplete bool              `json:"discoveryComplete"`
	UnsupportedKinds  []UnsupportedKind `json:"unsupportedKinds"`
	Limitations       []string          `json:"limitations,omitempty"`
}

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

func cloneCollectionCoverage(coverage *CollectionCoverage) *CollectionCoverage {
	if coverage == nil {
		return nil
	}
	copy := *coverage
	copy.UnsupportedKinds = slices.Clone(coverage.UnsupportedKinds)
	copy.Limitations = slices.Clone(coverage.Limitations)
	return &copy
}

func cloneSourceCapabilities(capabilities *SourceCapabilities) *SourceCapabilities {
	if capabilities == nil {
		return nil
	}
	copy := *capabilities
	if capabilities.MCPEnabled != nil {
		value := *capabilities.MCPEnabled
		copy.MCPEnabled = &value
	}
	if capabilities.ReadOnly != nil {
		value := *capabilities.ReadOnly
		copy.ReadOnly = &value
	}
	if capabilities.ExecEnabled != nil {
		value := *capabilities.ExecEnabled
		copy.ExecEnabled = &value
	}
	copy.AllowedOperations = slices.Clone(capabilities.AllowedOperations)
	copy.Dependencies = maps.Clone(capabilities.Dependencies)
	copy.Limitations = slices.Clone(capabilities.Limitations)
	return &copy
}
