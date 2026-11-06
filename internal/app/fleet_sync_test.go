package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/fleetsync"
)

func TestFleetSyncSkipsCloudDisabledIncompleteAndUnboundModes(t *testing.T) {
	validConfig := func() (fleetsync.ReporterConfig, bool, error) {
		return fleetsync.ReporterConfig{PortalURL: "http://localhost:9290", Token: strings.Repeat("t", 32), ClusterID: "east", Interval: 30 * time.Second, AllowHTTP: true}, true, nil
	}
	for _, test := range []struct {
		name      string
		cloudMode bool
		load      func() (fleetsync.ReporterConfig, bool, error)
		binding   string
		wantLoad  int
		wantStart bool
	}{
		{name: "cloud", cloudMode: true, load: validConfig, binding: "binding", wantLoad: 0},
		{name: "omitted", load: func() (fleetsync.ReporterConfig, bool, error) { return fleetsync.ReporterConfig{}, false, nil }, wantLoad: 1},
		{name: "invalid", load: func() (fleetsync.ReporterConfig, bool, error) {
			return fleetsync.ReporterConfig{}, false, errors.New("bad config")
		}, wantLoad: 1},
		{name: "unbound", load: validConfig, binding: "", wantLoad: 1},
		{name: "enabled", load: validConfig, binding: "binding", wantLoad: 1, wantStart: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			loadCalls, startCalls := 0, 0
			load := func() (fleetsync.ReporterConfig, bool, error) {
				loadCalls++
				return test.load()
			}
			started := startFleetSync(context.Background(), AppConfig{}, test.cloudMode, load,
				func(context.Context) string { return test.binding },
				func() string { return "context-a" },
				func() string { return "East cluster" },
				func(context.Context, string, string, string) (fleetsync.Snapshot, error) {
					return fleetsync.Snapshot{}, nil
				},
				func(context.Context, fleetsync.ReporterConfig, fleetsync.CaptureFunc, string) error {
					startCalls++
					return nil
				})
			if started != test.wantStart || loadCalls != test.wantLoad || startCalls != boolInt(test.wantStart) {
				t.Fatalf("started=%v loads=%d starts=%d", started, loadCalls, startCalls)
			}
		})
	}
}

func TestFleetSyncPinsBindingAndStartupContext(t *testing.T) {
	config := fleetsync.ReporterConfig{PortalURL: "http://localhost:9290", Token: strings.Repeat("t", 32), ClusterID: "east", Interval: 30 * time.Second, AllowHTTP: true}
	currentContext := "context-a"
	var capture fleetsync.CaptureFunc
	called := false
	started := startFleetSync(context.Background(), AppConfig{}, false,
		func() (fleetsync.ReporterConfig, bool, error) { return config, true, nil },
		func(context.Context) string { return "pinned-binding" },
		func() string { return currentContext },
		func() string { return "East cluster" },
		func(_ context.Context, clusterID, name, binding string) (fleetsync.Snapshot, error) {
			called = true
			if clusterID != "east" || name != "East cluster" || binding != "pinned-binding" {
				t.Fatalf("capture identity = %q/%q/%q", clusterID, name, binding)
			}
			return fleetsync.Snapshot{ClusterID: clusterID, Name: name}, nil
		},
		func(_ context.Context, got fleetsync.ReporterConfig, gotCapture fleetsync.CaptureFunc, _ string) error {
			if got.Name != "East cluster" {
				t.Fatalf("startup cluster name = %q", got.Name)
			}
			capture = gotCapture
			return nil
		})
	if !started || capture == nil {
		t.Fatal("Fleet reporter did not start")
	}
	if _, err := capture(context.Background()); err != nil || !called {
		t.Fatalf("pinned capture failed: called=%v err=%v", called, err)
	}
	called = false
	currentContext = "context-b"
	if _, err := capture(context.Background()); err == nil || called {
		t.Fatalf("capture retargeted after context switch: called=%v err=%v", called, err)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
