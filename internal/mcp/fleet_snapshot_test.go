package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/internal/k8s"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func setupFleetSnapshotTest(t *testing.T) (context.Context, string) {
	t.Helper()
	t.Cleanup(k8s.ResetTestState)
	t.Cleanup(k8s.SetTestLocalMode())
	t.Cleanup(k8s.SetTestProfileSource("/fake/kubeconfig", "fleet-test", ""))
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default", Labels: map[string]string{"app": "api"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example.invalid/api:v1"}}},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "private", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("snapshot-test-secret-value")},
	}
	if err := k8s.InitTestResourceCache(fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		pod,
		secret,
	)); err != nil {
		t.Fatalf("initialize fixture cache: %v", err)
	}
	return context.Background(), k8s.ClusterSafetyBinding(context.Background())
}

func TestCaptureFleetSnapshotPreservesCanonicalResourceAndExcludesSecretData(t *testing.T) {
	ctx, binding := setupFleetSnapshotTest(t)
	if binding == "" {
		t.Fatal("fixture connection did not provide a binding")
	}
	direct, _, err := handleGetResource(ctx, nil, getResourceInput{
		Kind: "pods", Namespace: "default", Name: "api",
	})
	if err != nil {
		t.Fatalf("canonical get_resource: %v", err)
	}
	if direct == nil || len(direct.Content) != 1 {
		t.Fatalf("canonical get_resource result = %+v", direct)
	}
	directText, ok := direct.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("canonical result content type = %T", direct.Content[0])
	}

	snapshot, err := CaptureFleetSnapshot(ctx, "east", "East", binding)
	if err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}
	var captured []byte
	for _, document := range snapshot.Documents {
		if document.Tool != "get_resource" {
			continue
		}
		var input getResourceInput
		if err := json.Unmarshal(document.Input, &input); err != nil {
			t.Fatal(err)
		}
		if input.Kind == "pods" && input.Namespace == "default" && input.Name == "api" {
			captured = document.Data
		}
		if strings.EqualFold(input.Kind, "secret") || strings.EqualFold(input.Kind, "secrets") {
			t.Fatalf("snapshot includes a Secret get_resource document: %+v", input)
		}
	}
	if string(captured) != directText.Text {
		t.Fatalf("captured canonical payload changed:\nwant %s\ngot  %s", directText.Text, captured)
	}
	serialized, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "snapshot-test-secret-value") {
		t.Fatal("snapshot contains Secret data")
	}
}

func TestCaptureFleetSnapshotRejectsCanceledCapture(t *testing.T) {
	ctx, binding := setupFleetSnapshotTest(t)
	if binding == "" {
		t.Fatal("fixture connection did not provide a binding")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := CaptureFleetSnapshot(canceled, "east", "East", binding); err == nil {
		t.Fatal("canceled capture returned a snapshot")
	}
}

func TestCaptureFleetSnapshotSkipsUnsupportedInventories(t *testing.T) {
	ctx, binding := setupFleetSnapshotTest(t)
	fixture := &collectionDiscoveryFixture{lists: []*metav1.APIResourceList{
		{GroupVersion: "v1", APIResources: []metav1.APIResource{
			{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
			{Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
			{Name: "componentstatuses", Kind: "ComponentStatus", Verbs: []string{"get", "list"}},
		}},
		{GroupVersion: "metrics.k8s.io/v1beta1", APIResources: []metav1.APIResource{{Name: "pods", Kind: "PodMetrics", Namespaced: true, Verbs: []string{"get", "list"}}}},
	}}
	restore, err := k8s.SetTestResourceDiscovery(fixture)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	snapshot, err := CaptureFleetSnapshot(ctx, "east", "East", binding)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Coverage == nil || !snapshot.Coverage.DiscoveryComplete || snapshot.Capabilities != nil {
		t.Fatal("missing collection evidence or invented runtime capabilities")
	}
	foundPod := false
	for _, document := range snapshot.Documents {
		if document.Tool != "list_resources" && document.Tool != "get_resource" {
			continue
		}
		var input listResourcesInput
		if err := json.Unmarshal(document.Input, &input); err != nil {
			t.Fatal(err)
		}
		if input.Kind != "pods" || input.Group != "" {
			t.Fatalf("unsupported/secret inventory became a document: %s %s", document.Tool, document.Input)
		}
		if document.Error != "" {
			t.Fatalf("supported pod read failed: %s", document.Error)
		}
		foundPod = true
	}
	if !foundPod {
		t.Fatal("supported inventory missing")
	}
	var skipped []string
	for _, kind := range snapshot.Coverage.UnsupportedKinds {
		skipped = append(skipped, kind.Group+"/"+kind.Resource+":"+kind.Reason)
	}
	for _, want := range []string{"/componentstatuses:missing_verbs:watch", "metrics.k8s.io/pods:missing_verbs:watch", "resource.k8s.io/resourceclaims:api_not_served"} {
		if !slices.Contains(skipped, want) {
			t.Fatalf("missing skip evidence %s", want)
		}
	}
}
