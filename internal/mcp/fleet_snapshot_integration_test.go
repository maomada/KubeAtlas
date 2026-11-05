package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/maomada/KubeAtlas/internal/fleetsync"
	"github.com/maomada/KubeAtlas/internal/k8s"
	"github.com/maomada/KubeAtlas/internal/portal"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type snapshotStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *snapshotStatusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *snapshotStatusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func TestFleetSnapshotReporterManagerMCPIntegration(t *testing.T) {
	t.Cleanup(k8s.ResetTestState)
	store, err := fleetsync.OpenStore(filepath.Join(t.TempDir(), "fleet-state", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	registry, err := portal.OpenRegistry(filepath.Join(t.TempDir(), "portal", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{
		"east": "east-registration-token-with-at-least-32-bytes",
		"west": "west-registration-token-with-at-least-32-bytes",
	}
	handler, err := portal.NewHandler(registry, portal.Credentials{
		Tokens: tokens, Username: "fleet-viewer", Password: "fleet-viewer-password-long",
		AllowInsecureCookies: true,
	}, fstest.MapFS{"portal.html": &fstest.MapFile{Data: []byte("<html>Portal</html>")}}, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handler.Close)
	snapshotResponses := make(chan int, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/fleet/snapshots/") {
			tracked := &snapshotStatusWriter{ResponseWriter: w}
			handler.ServeHTTP(tracked, r)
			snapshotResponses <- tracked.status
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	viewerClient := &http.Client{Jar: jar}
	loginBody := bytes.NewBufferString(`{"username":"fleet-viewer","password":"fleet-viewer-password-long"}`)
	login, err := viewerClient.Post(server.URL+"/api/auth/login", "application/json", loginBody)
	if err != nil {
		t.Fatal(err)
	}
	_ = login.Body.Close()
	if login.StatusCode != http.StatusNoContent {
		t.Fatalf("Portal viewer login status = %d", login.StatusCode)
	}

	captureAndReport := func(clusterID, source, contextName, image string) (fleetsync.Snapshot, string) {
		t.Helper()
		k8s.ResetTestState()
		k8s.SetTestLocalMode()
		k8s.SetTestProfileSource(source, contextName, "")
		ctx := context.Background()
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "api", Namespace: "default",
				Labels: map[string]string{"app": "api", "fixture-source": clusterID},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: image}}},
		}
		if err := k8s.InitTestResourceCache(fake.NewSimpleClientset(
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
			pod,
		)); err != nil {
			t.Fatalf("initialize %s fixture cache: %v", clusterID, err)
		}
		binding := k8s.ClusterSafetyBinding(ctx)
		if binding == "" {
			t.Fatalf("%s fixture did not produce a cluster safety binding", clusterID)
		}

		direct, _, err := handleGetResource(ctx, nil, getResourceInput{
			Kind: "pods", Namespace: "default", Name: "api",
		})
		if err != nil || direct == nil || len(direct.Content) != 1 {
			t.Fatalf("%s canonical get_resource: result=%+v err=%v", clusterID, direct, err)
		}
		directText, ok := direct.Content[0].(*mcpsdk.TextContent)
		if !ok {
			t.Fatalf("%s canonical content type = %T", clusterID, direct.Content[0])
		}

		captured := make(chan fleetsync.Snapshot, 1)
		reporter, err := fleetsync.NewReporter(fleetsync.ReporterConfig{
			PortalURL: server.URL, Token: tokens[clusterID], ClusterID: clusterID, Name: contextName,
			Interval: 5 * time.Second, AllowHTTP: true,
		}, func(captureCtx context.Context) (fleetsync.Snapshot, error) {
			snapshot, err := CaptureFleetSnapshot(captureCtx, clusterID, contextName, binding)
			if err == nil {
				captured <- snapshot
			}
			return snapshot, err
		})
		if err != nil {
			t.Fatalf("create %s reporter: %v", clusterID, err)
		}
		reportCtx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			reporter.Run(reportCtx, nil)
		}()
		select {
		case status := <-snapshotResponses:
			if status != http.StatusOK {
				cancel()
				<-done
				t.Fatalf("%s snapshot ingest status = %d", clusterID, status)
			}
		case <-time.After(15 * time.Second):
			cancel()
			<-done
			t.Fatalf("%s reporter did not submit its snapshot", clusterID)
		}
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s reporter did not stop after cancellation", clusterID)
		}
		snapshot := <-captured
		return snapshot, directText.Text
	}

	east, eastResource := captureAndReport("east", "/fake/east.kubeconfig", "east-context", "example.invalid/api:east")
	west, westResource := captureAndReport("west", "/fake/west.kubeconfig", "west-context", "example.invalid/api:west")
	if east.SourceID == west.SourceID || east.ClusterID == west.ClusterID {
		t.Fatal("independent source fixtures did not retain distinct identities")
	}

	putGroup := func(group fleetsync.Group) {
		t.Helper()
		body, err := json.Marshal(group)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPut, server.URL+"/api/fleet/fleets/"+group.ID, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := viewerClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("PUT group status %d: %s", response.StatusCode, body)
		}
	}
	putGroup(fleetsync.Group{ID: "multi-cloud", Name: "Multi-cloud", ClusterIDs: []string{"east", "west"}})

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "fleet-snapshot-integration-test", Version: "1"}, nil)
	mcpCtx, mcpCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer mcpCancel()
	session, err := client.Connect(mcpCtx, &mcpsdk.StreamableClientTransport{
		Endpoint: server.URL + "/mcp-fleet", HTTPClient: viewerClient, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	callJSON := func(name string, arguments map[string]any, result any) {
		t.Helper()
		call, err := session.CallTool(mcpCtx, &mcpsdk.CallToolParams{Name: name, Arguments: arguments})
		if err != nil || call.IsError || len(call.Content) != 1 {
			t.Fatalf("%s result = %+v, err = %v", name, call, err)
		}
		content, ok := call.Content[0].(*mcpsdk.TextContent)
		if !ok {
			t.Fatalf("%s content type = %T", name, call.Content[0])
		}
		if err := json.Unmarshal([]byte(content.Text), result); err != nil {
			t.Fatalf("%s JSON: %v", name, err)
		}
	}

	var grouped fleetsync.AgentContext
	callJSON("get_agent_context", map[string]any{"fleetId": "multi-cloud"}, &grouped)
	if grouped.Mode != "multi_cloud" || len(grouped.Clusters) != 2 {
		t.Fatalf("initial grouped context = %+v", grouped)
	}
	snapshots := map[string]fleetsync.Snapshot{"east": east, "west": west}
	for _, cluster := range grouped.Clusters {
		snapshot, ok := snapshots[cluster.ClusterID]
		if !ok {
			t.Fatalf("unexpected group member %q", cluster.ClusterID)
		}
		expectedCount := 0
		for _, expected := range snapshot.Documents {
			if expected.Tool == "get_resource" {
				continue
			}
			expectedCount++
			matched := false
			for _, got := range cluster.Documents {
				if got.Tool == expected.Tool && bytes.Equal(got.Input, expected.Input) {
					matched = bytes.Equal(got.Data, expected.Data) && got.Error == expected.Error
					break
				}
			}
			if !matched {
				t.Fatalf("MCP group changed %s payload for cluster %s", expected.Tool, cluster.ClusterID)
			}
		}
		if len(cluster.Documents) != expectedCount {
			t.Fatalf("cluster %s documents = %d, want %d", cluster.ClusterID, len(cluster.Documents), expectedCount)
		}
	}

	for clusterID, canonical := range map[string]string{"east": eastResource, "west": westResource} {
		var resource fleetsync.ClusterContext
		arguments := map[string]any{
			"clusterId": clusterID, "kind": "pods", "namespace": "default", "name": "api",
		}
		callJSON("get_resource_context", arguments, &resource)
		if resource.ClusterID != clusterID || len(resource.Documents) != 1 ||
			string(resource.Documents[0].Data) != canonical {
			t.Fatalf("%s resource context did not preserve canonical get_resource: %+v", clusterID, resource)
		}
		requestBody, err := json.Marshal(fleetsync.ResourceSelection{
			ClusterID: clusterID, Kind: "pods", Namespace: "default", Name: "api",
		})
		if err != nil {
			t.Fatal(err)
		}
		restRequest, err := http.NewRequest(http.MethodPost, server.URL+"/api/fleet/resource-context", bytes.NewReader(requestBody))
		if err != nil {
			t.Fatal(err)
		}
		restRequest.Header.Set("Content-Type", "application/json")
		restResponse, err := viewerClient.Do(restRequest)
		if err != nil {
			t.Fatal(err)
		}
		if restResponse.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(restResponse.Body)
			_ = restResponse.Body.Close()
			t.Fatalf("%s REST resource context status = %d: %s", clusterID, restResponse.StatusCode, body)
		}
		var restResource fleetsync.ClusterContext
		err = json.NewDecoder(restResponse.Body).Decode(&restResource)
		_ = restResponse.Body.Close()
		if err != nil || restResource.ClusterID != clusterID || len(restResource.Documents) != 1 ||
			string(restResource.Documents[0].Data) != canonical {
			t.Fatalf("%s REST resource context changed canonical payload: %+v err=%v", clusterID, restResource, err)
		}
	}
	if eastResource == westResource {
		t.Fatal("fixture resources did not distinguish the two cluster payloads")
	}

	putGroup(fleetsync.Group{ID: "multi-cloud", Name: "Multi-cloud", ClusterIDs: []string{"west"}})
	var edited fleetsync.AgentContext
	callJSON("get_agent_context", map[string]any{"fleetId": "multi-cloud"}, &edited)
	if edited.Mode != "multi_cloud" || len(edited.Clusters) != 1 || edited.Clusters[0].ClusterID != "west" {
		t.Fatalf("edited grouped context did not shrink: %+v", edited)
	}
	if len(store.Inventory()) != 2 {
		t.Fatalf("editing the group changed cluster inventory: %s", fmt.Sprint(store.Inventory()))
	}
}
