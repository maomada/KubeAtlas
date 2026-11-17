package gitops

import (
	"context"
	"github.com/maomada/KubeAtlas/pkg/compat"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
)

func TestDeployedPauseStateSurvivesBrandingAndRepeatedPause(t *testing.T) {
	app := argoAppForTest("fixture", "app", nil)
	app.SetAnnotations(map[string]string{compat.LegacyArgoPruneAnnotation: "false", compat.LegacyArgoSelfHealAnnotation: "true"})
	client := newFakeArgo(app)
	if _, err := SetArgoAutoSync(context.Background(), client, "fixture", "app", false); err != nil {
		t.Fatal(err)
	}
	if _, err := SetArgoAutoSync(context.Background(), client, "fixture", "app", true); err != nil {
		t.Fatal(err)
	}
	current, err := client.Resource(argoAppGVR).Namespace("fixture").Get(context.Background(), "app", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prune, _, _ := unstructured.NestedBool(current.Object, "spec", "syncPolicy", "automated", "prune")
	selfHeal, _, _ := unstructured.NestedBool(current.Object, "spec", "syncPolicy", "automated", "selfHeal")
	if prune || !selfHeal {
		t.Fatal("saved pre-pause settings changed")
	}
	for _, key := range []string{ArgoSuspendedPruneAnnotation, ArgoSuspendedSelfHealAnnotation, compat.LegacyArgoPruneAnnotation, compat.LegacyArgoSelfHealAnnotation} {
		if _, ok := current.GetAnnotations()[key]; ok {
			t.Fatalf("owned annotation %s retained", key)
		}
	}
}

func TestArgoValidationCompatibilityRejectsConflictingOperationNonce(t *testing.T) {
	operation := func(entries ...any) map[string]any { return map[string]any{"info": entries} }
	entry := func(name, value string) any { return map[string]any{"name": name, "value": value} }
	if !argoOperationHasInfo(operation(entry(compat.LegacyArgoValidationInfo, "expected")), "kubeatlas-validation-id", "expected") {
		t.Fatal("old in-flight operation lost")
	}
	if argoOperationHasInfo(operation(entry(compat.LegacyArgoValidationInfo, "expected"), entry("kubeatlas-validation-id", "other")), "kubeatlas-validation-id", "expected") {
		t.Fatal("conflicting operation nonce accepted")
	}
}
