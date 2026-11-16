package opencost

import (
	"errors"
	"time"
)

type Status struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
	Source Source `json:"source,omitempty"`
}

// StatusSnapshot reports cached selection, not cost data availability. Unlike
// Selected it must not refresh configuration, probe, or consult a lease.
func StatusSnapshot() Status { return defaultManager.statusSnapshot(time.Now()) }

func (m *Manager) statusSnapshot(now time.Time) Status {
	if m == nil {
		return Status{State: "unknown", Reason: "not_probed"}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := Status{State: "unknown", Reason: "not_probed", Source: statusSource(m.config.Source)}
	if m.envError != "" {
		status.State, status.Reason = "unavailable", "invalid_configuration"
		return status
	}
	if m.selectionErr != nil {
		if !now.Before(m.retryAt) {
			status.Reason = "cached_failure_expired"
			return status
		}
		status.State, status.Reason = "unavailable", "cost_source_unavailable"
		if errors.Is(m.selectionErr, ErrNoCostSource) {
			status.Reason = "no_cost_source"
		}
		return status
	}
	if source := statusSource(m.selected); source != "" {
		status.State, status.Reason, status.Source = "selected", "selection_not_data_availability", source
	}
	return status
}

func statusSource(source Source) Source {
	switch source {
	case SourceAuto, SourcePrometheus, SourceKubecost:
		return source
	default:
		return ""
	}
}
