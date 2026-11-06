package fleetsync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	config := reporterTestConfig("http://placeholder")
	var gotPath, gotAuthorization string
	snapshotHandler := NewSnapshotHandler(store, "east")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		snapshotHandler.ServeHTTP(w, r)
	}))
	defer server.Close()
	config.PortalURL = server.URL
	snapshot := sampleCluster("east", time.Now().UTC()).Snapshot
	reporter, err := NewReporter(config, func(context.Context) (Snapshot, error) { return snapshot, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := reporter.postSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/fleet/snapshots/east" || gotAuthorization != "Bearer "+config.Token {
		t.Fatalf("upload route/auth = %q/%q", gotPath, gotAuthorization)
	}
	inventory := store.Inventory()
	if len(inventory) != 1 || inventory[0].ClusterID != "east" || inventory[0].ObservedAt != snapshot.ObservedAt {
		t.Fatalf("manager inventory = %+v", inventory)
	}
}

func TestReporterConfigIsOptInAndRejectsIncompleteOrUnsafeURLs(t *testing.T) {
	for _, key := range []string{
		"KUBEATLAS_FLEET_SYNC_ENABLED", "KUBEATLAS_PORTAL_URL", "KUBEATLAS_PORTAL_CLUSTER_ID",
		"KUBEATLAS_PORTAL_CLUSTER_NAME", "KUBEATLAS_PORTAL_TOKEN", "KUBEATLAS_PORTAL_ALLOW_HTTP",
		"KUBEATLAS_FLEET_SYNC_INTERVAL",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("KUBEATLAS_FLEET_URL", "https://old-manager.example")
	t.Setenv("KUBEATLAS_FLEET_CLUSTER_ID", "old-cluster")
	if _, enabled, err := ConfigFromEnv(); err != nil || enabled {
		t.Fatalf("empty config: enabled=%v err=%v", enabled, err)
	}
	t.Setenv("KUBEATLAS_FLEET_SYNC_ENABLED", "true")
	if _, enabled, err := ConfigFromEnv(); err == nil || enabled {
		t.Fatalf("incomplete config: enabled=%v err=%v", enabled, err)
	}
	t.Setenv("KUBEATLAS_PORTAL_URL", "https://portal.example")
	t.Setenv("KUBEATLAS_PORTAL_CLUSTER_ID", "east")
	t.Setenv("KUBEATLAS_PORTAL_TOKEN", strings.Repeat("t", 32))
	config, enabled, err := ConfigFromEnv()
	if err != nil || !enabled || config.Interval != defaultSyncInterval || config.ClusterID != "east" {
		t.Fatalf("valid config = %+v enabled=%v err=%v", config, enabled, err)
	}
	for _, rawURL := range []string{
		"https://user:pass@manager.example", "https://manager.example/?x=1",
		"https://manager.example/#fragment", "ftp://manager.example",
	} {
		t.Setenv("KUBEATLAS_PORTAL_URL", rawURL)
		if _, enabled, err := ConfigFromEnv(); err == nil || enabled {
			t.Errorf("accepted unsafe URL %q: enabled=%v err=%v", rawURL, enabled, err)
		}
	}
	t.Setenv("KUBEATLAS_PORTAL_URL", "http://localhost")
	if _, enabled, err := ConfigFromEnv(); err == nil || enabled {
		t.Fatalf("HTTP without opt-in accepted: enabled=%v err=%v", enabled, err)
	}
	t.Setenv("KUBEATLAS_PORTAL_ALLOW_HTTP", "true")
	if _, enabled, err := ConfigFromEnv(); err != nil || !enabled {
		t.Fatalf("explicit HTTP allowance rejected: enabled=%v err=%v", enabled, err)
	}
}

func TestReporterDoesNotPostFailedCaptureAndUsesGenericError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer server.Close()
	reporter, err := NewReporter(reporterTestConfig(server.URL), func(context.Context) (Snapshot, error) {
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
	reporter, err := NewReporter(reporterTestConfig(redirect.URL), func(context.Context) (Snapshot, error) { return Snapshot{}, nil })
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
	reporter, err := NewReporter(reporterTestConfig(server.URL), func(ctx context.Context) (Snapshot, error) {
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

func reporterTestConfig(portalURL string) ReporterConfig {
	return ReporterConfig{
		PortalURL: portalURL, Token: strings.Repeat("t", 32), ClusterID: "east",
		Name: "east", Interval: minimumSyncInterval, AllowHTTP: true,
	}
}
