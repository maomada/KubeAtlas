package opencost

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestStatusSnapshotCachedSelectionOnly(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, state, reason string
		selected            Source
		failure             error
		retryAt             time.Time
		envError            string
	}{
		{name: "not probed", state: "unknown", reason: "not_probed"},
		{name: "forced prometheus is not metrics proof", selected: SourcePrometheus, state: "selected", reason: "selection_not_data_availability"},
		{name: "cached kubecost", selected: SourceKubecost, state: "selected", reason: "selection_not_data_availability"},
		{name: "missing source", failure: fmt.Errorf("private-url: %w", ErrNoCostSource), retryAt: now.Add(time.Second), state: "unavailable", reason: "no_cost_source"},
		{name: "failure", failure: errors.New("private-error"), retryAt: now.Add(time.Second), state: "unavailable", reason: "cost_source_unavailable"},
		{name: "expired failure even with selection", selected: SourcePrometheus, failure: ErrNoCostSource, retryAt: now, state: "unknown", reason: "cached_failure_expired"},
		{name: "invalid env", selected: SourcePrometheus, envError: "private-env", state: "unavailable", reason: "invalid_configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manager{config: ManagerConfig{Source: SourceAuto, URL: "private-url", APIKey: "private-key", ClusterID: "private-id"}, selected: tc.selected, selectionErr: tc.failure, retryAt: tc.retryAt, envError: tc.envError,
				lease: &connectionLease{alive: func() bool { t.Fatal("status consulted lease"); return false }, release: func() { t.Fatal("status released lease") }}}
			got := m.statusSnapshot(now)
			if got.State != tc.state || got.Reason != tc.reason {
				t.Fatalf("status = %+v; want %s/%s", got, tc.state, tc.reason)
			}
			wantSource := SourceAuto
			if tc.state == "selected" {
				wantSource = tc.selected
			}
			if got.Source != wantSource {
				t.Fatalf("source = %q, want %q", got.Source, wantSource)
			}
			raw, err := json.Marshal(got)
			if err != nil || strings.Contains(string(raw), "private-") {
				t.Fatalf("unsafe status: %s (%v)", raw, err)
			}
			if m.selected != tc.selected || m.selectionErr != tc.failure || m.retryAt != tc.retryAt {
				t.Fatal("status changed selection state")
			}
		})
	}
	if got := (*Manager)(nil).statusSnapshot(now); got.State != "unknown" {
		t.Fatalf("nil manager = %+v", got)
	}
	if got := (&Manager{config: ManagerConfig{Source: "private-config"}, selected: "private-selected"}).statusSnapshot(now); got.Source != "" {
		t.Fatal("non-enum source escaped")
	}
}
