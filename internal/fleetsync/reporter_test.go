package fleetsync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestReporterUploadsFullSnapshotToManager(t *testing.T) {
	store, err := OpenStore(testStatePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := httptest.NewServer(NewHandler(store))
	defer server.Close()
	snapshot := sampleCluster("east", time.Now().UTC()).Snapshot
	reporter, err := NewReporter(ReporterConfig{
		URL: server.URL, ClusterID: "east", Name: "east", Interval: minimumSyncInterval,
	}, func(context.Context) (Snapshot, error) { return snapshot, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := reporter.postSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	inventory := store.Inventory()
	if len(inventory) != 1 || inventory[0].ClusterID != "east" || inventory[0].ObservedAt != snapshot.ObservedAt {
		t.Fatalf("manager inventory = %+v", inventory)
	}
}

func TestReporterConfigIsOptInAndRejectsIncompleteOrUnsafeURLs(t *testing.T) {
	for _, key := range []string{
		"KUBEATLAS_FLEET_URL", "KUBEATLAS_FLEET_CLUSTER_ID", "KUBEATLAS_FLEET_CLUSTER_NAME",
		"KUBEATLAS_FLEET_SYNC_INTERVAL",
	} {
		t.Setenv(key, "")
	}
	if _, enabled, err := ConfigFromEnv(); err != nil || enabled {
		t.Fatalf("empty config: enabled=%v err=%v", enabled, err)
	}
	t.Setenv("KUBEATLAS_FLEET_CLUSTER_ID", "east")
	if _, enabled, err := ConfigFromEnv(); err == nil || enabled {
		t.Fatalf("incomplete config: enabled=%v err=%v", enabled, err)
	}
	t.Setenv("KUBEATLAS_FLEET_URL", "https://manager.example")
	t.Setenv("KUBEATLAS_FLEET_CLUSTER_ID", "east")
	config, enabled, err := ConfigFromEnv()
	if err != nil || !enabled || config.Interval != defaultSyncInterval || config.ClusterID != "east" {
		t.Fatalf("valid config = %+v enabled=%v err=%v", config, enabled, err)
	}
	for _, rawURL := range []string{
		"https://user:pass@manager.example", "https://manager.example/?x=1",
		"https://manager.example/#fragment", "ftp://manager.example",
	} {
		t.Setenv("KUBEATLAS_FLEET_URL", rawURL)
		if _, enabled, err := ConfigFromEnv(); err == nil || !enabled {
			t.Errorf("accepted unsafe URL %q: enabled=%v err=%v", rawURL, enabled, err)
		}
	}
}

func TestReporterDoesNotPostFailedCaptureAndUsesGenericError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer server.Close()
	reporter, err := NewReporter(ReporterConfig{
		URL: server.URL, ClusterID: "east", Name: "east", Interval: minimumSyncInterval,
	}, func(context.Context) (Snapshot, error) {
		return Snapshot{}, errors.New("private cluster payload detail")
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errorSeen := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		reporter.Run(ctx, func(err error) {
			errorSeen <- err
			cancel()
		})
		close(done)
	}()
	select {
	case err := <-errorSeen:
		if err.Error() != "Fleet sync attempt failed" {
			t.Fatalf("reporter exposed detail: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capture failure was not reported")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reporter did not stop after cancellation")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("capture failure uploaded %d requests", got)
	}
}

func TestReporterDoesNotFollowRedirects(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	reporter, err := NewReporter(ReporterConfig{
		URL: redirect.URL, ClusterID: "east", Name: "east", Interval: minimumSyncInterval,
	}, func(context.Context) (Snapshot, error) { return Snapshot{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	snapshot := sampleCluster("east", time.Now().UTC()).Snapshot
	if err := reporter.postSnapshot(context.Background(), snapshot); err == nil {
		t.Fatal("redirect response was accepted")
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("redirect target received %d requests", got)
	}
}

func TestReporterCancellationStopsCaptureWithoutUploading(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	started := make(chan struct{})
	reporter, err := NewReporter(ReporterConfig{
		URL: server.URL, ClusterID: "east", Name: "east", Interval: minimumSyncInterval,
	}, func(ctx context.Context) (Snapshot, error) {
		close(started)
		<-ctx.Done()
		return Snapshot{}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reporter.Run(ctx, nil)
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reporter did not stop")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("canceled capture uploaded %d requests", got)
	}
}
