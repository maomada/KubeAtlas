package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestFleetSnapshotRelationshipFactsUseOriginalIPsAndUIDs(t *testing.T) {
	t.Cleanup(k8s.ResetTestState)
	t.Cleanup(k8s.SetTestLocalMode())
	t.Cleanup(k8s.SetTestProfileSource("/fake/relationship-kubeconfig", "relationship-test", ""))
	if err := k8s.InitTestResourceCache(fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system", UID: "fixture-system-uid"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "example.invalid/api:v1"}}}, Status: corev1.PodStatus{PodIP: "10.70.0.9"}},
	)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snapshot, err := CaptureFleetSnapshot(ctx, "east", "East", k8s.ClusterSafetyBinding(ctx))
	if err != nil {
		t.Fatal(err)
	}
	foundIP, foundIdentity := false, false
	for _, fact := range snapshot.RelationshipFacts {
		foundIP = foundIP || fact.Type == "address" && fact.Value == "10.70.0.9" && fact.Resource.Kind == "pods"
		foundIdentity = foundIdentity || fact.Type == "cluster_identity" && fact.Value == "fixture-system-uid"
	}
	if !foundIP || !foundIdentity {
		t.Fatalf("lost original facts: %+v", snapshot.RelationshipFacts)
	}
	for _, doc := range snapshot.Documents {
		if doc.Tool != "get_resource" {
			continue
		}
		var input getResourceInput
		if err := json.Unmarshal(doc.Input, &input); err != nil {
			t.Fatal(err)
		}
		if input.Kind == "pods" && strings.Contains(string(doc.Data), `"podIP"`) {
			t.Fatal("original MCP projection was changed to add IP")
		}
		if input.Kind == "namespaces" && strings.Contains(string(doc.Data), "fixture-system-uid") {
			t.Fatal("original MCP projection was changed to add UID")
		}
	}
}
