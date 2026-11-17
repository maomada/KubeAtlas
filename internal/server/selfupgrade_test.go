package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/maomada/KubeAtlas/internal/auth"
)

// TestSelfUpgradePatchOptions is a tripwire: handleSelfUpgrade must use
// Server-Side Apply as Helm's field manager and force-conflicts to reclaim
// .image from old strategic-merge self-upgrades.
func TestSelfUpgradePatchOptions(t *testing.T) {
	opts := selfUpgradePatchOptions()
	if opts.FieldManager != "helm" {
		t.Errorf("FieldManager = %q, want %q", opts.FieldManager, "helm")
	}
	if opts.Force == nil || !*opts.Force {
		t.Errorf("Force = %v, want non-nil true", opts.Force)
	}
}

func TestSelfUpgradeRequiresCloudOwner(t *testing.T) {
	t.Setenv("MY_POD_NAMESPACE", "")
	t.Setenv("MY_DEPLOYMENT_NAME", "")

	for _, tier := range []string{"kubeatlas:viewer", "kubeatlas:member", "kubeatlas:unknown", ""} {
		t.Run(tier, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/agent/self-upgrade", strings.NewReader(`{"image":"ghcr.io/maomada/kubeatlas:1.9.0"}`))
			req = req.WithContext(auth.ContextWithUser(req.Context(), userWithGroups(tier)))
			rec := httptest.NewRecorder()

			(&Server{}).handleSelfUpgrade(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["error_code"] != auth.ErrCodeCloudRoleInsufficient {
				t.Fatalf("error_code = %q, want %q", body["error_code"], auth.ErrCodeCloudRoleInsufficient)
			}
		})
	}
}

func TestSelfUpgradeImagePreservesConfiguredRepository(t *testing.T) {
	tests := []struct {
		name       string
		containers []corev1.Container
		want       string
		wantErr    bool
	}{
		{
			name:       "official repository",
			containers: []corev1.Container{{Name: "kubeatlas", Image: "ghcr.io/maomada/kubeatlas:1.8.0"}},
			want:       "ghcr.io/maomada/kubeatlas:1.9.0",
		},
		{
			name:       "private registry with port",
			containers: []corev1.Container{{Name: "kubeatlas", Image: "registry.example:5000/platform/kubeatlas:1.8.0"}},
			want:       "registry.example:5000/platform/kubeatlas:1.9.0",
		},
		{
			name:       "digest pin",
			containers: []corev1.Container{{Name: "kubeatlas", Image: "mirror.example/kubeatlas@sha256:abc"}},
			want:       "mirror.example/kubeatlas:1.9.0",
		},
		{
			name:       "missing KubeAtlas container",
			containers: []corev1.Container{{Name: "sidecar", Image: "example.com/sidecar:v1"}},
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selfUpgradeImage(tt.containers, "1.9.0")
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("selfUpgradeImage() = (%q, %v), want (%q, err=%v)", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestSelfUpgradeCloudOwnerPassesRoleGate(t *testing.T) {
	t.Setenv("MY_POD_NAMESPACE", "")
	t.Setenv("MY_DEPLOYMENT_NAME", "")
	req := httptest.NewRequest(http.MethodPost, "/api/agent/self-upgrade", strings.NewReader(`{"image":"ghcr.io/maomada/kubeatlas:1.9.0"}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(), userWithGroups("kubeatlas:owner")))
	rec := httptest.NewRecorder()

	(&Server{}).handleSelfUpgrade(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 after owner passes role gate; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), auth.ErrCodeCloudRoleInsufficient) {
		t.Fatalf("owner was rejected by Cloud role gate: %s", rec.Body.String())
	}
}

func TestIsValidKubeAtlasImageTag(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want bool
	}{
		{name: "semver", tag: "1.6.2", want: true},
		{name: "underscore and dash", tag: "v1_6-rc.1", want: true},
		{name: "empty", tag: "", want: false},
		{name: "too long", tag: "12345678901234567890123456789012345678901234567890123456789012345", want: false},
		{name: "starts with dot", tag: ".1.6.2", want: false},
		{name: "newline injection", tag: "1.6.2\nlevel=error", want: false},
		{name: "space injection", tag: "1.6.2 level=error", want: false},
		{name: "slash", tag: "release/1.6.2", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidKubeAtlasImageTag(tt.tag); got != tt.want {
				t.Fatalf("isValidKubeAtlasImageTag(%q) = %v, want %v", tt.tag, got, tt.want)
			}
		})
	}
}

func TestBuildSelfUpgradeApplyPatchUsesFullHelmDeploymentManifest(t *testing.T) {
	manifest := `---
# Source: kubeatlas/templates/service.yaml
apiVersion: v1
kind: Service
metadata:
  name: kubeatlas
---
# Source: kubeatlas/templates/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kubeatlas
  labels:
    app.kubernetes.io/name: kubeatlas
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: kubeatlas
      app.kubernetes.io/instance: kubeatlas
  template:
    metadata:
      labels:
        app.kubernetes.io/name: kubeatlas
        app.kubernetes.io/instance: kubeatlas
    spec:
      serviceAccountName: kubeatlas
      containers:
        - name: kubeatlas
          image: "ghcr.io/maomada/kubeatlas:1.6.1"
          imagePullPolicy: IfNotPresent
        - name: sidecar
          image: "example.com/sidecar:v1"
`

	patch, err := buildSelfUpgradeApplyPatch(manifest, "kubeatlas", "kubeatlas", "kubeatlas", "ghcr.io/maomada/kubeatlas:1.6.2")
	if err != nil {
		t.Fatalf("buildSelfUpgradeApplyPatch() error = %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(patch, &obj); err != nil {
		t.Fatalf("patch is not valid JSON: %v", err)
	}

	if got := nestedString(t, obj, "metadata", "namespace"); got != "kubeatlas" {
		t.Errorf("metadata.namespace = %q, want kubeatlas", got)
	}
	if got := nestedString(t, obj, "metadata", "annotations", "meta.helm.sh/release-name"); got != "kubeatlas" {
		t.Errorf("release-name annotation = %q, want kubeatlas", got)
	}
	if got := nestedString(t, obj, "metadata", "annotations", "meta.helm.sh/release-namespace"); got != "kubeatlas" {
		t.Errorf("release-namespace annotation = %q, want kubeatlas", got)
	}
	if got := nestedFloat(t, obj, "spec", "replicas"); got != 1 {
		t.Errorf("spec.replicas = %v, want 1", got)
	}
	if got := nestedString(t, obj, "spec", "selector", "matchLabels", "app.kubernetes.io/name"); got != "kubeatlas" {
		t.Errorf("selector label = %q, want kubeatlas", got)
	}
	if got := nestedString(t, obj, "spec", "template", "spec", "serviceAccountName"); got != "kubeatlas" {
		t.Errorf("serviceAccountName = %q, want kubeatlas", got)
	}

	containers, ok, err := nestedSlice(obj, "spec", "template", "spec", "containers")
	if err != nil || !ok {
		t.Fatalf("containers lookup ok=%v err=%v", ok, err)
	}
	if got := containers[0].(map[string]any)["image"]; got != "ghcr.io/maomada/kubeatlas:1.6.2" {
		t.Errorf("kubeatlas image = %q, want upgraded image", got)
	}
	if got := containers[0].(map[string]any)["imagePullPolicy"]; got != "IfNotPresent" {
		t.Errorf("kubeatlas imagePullPolicy = %q, want preserved", got)
	}
	if got := containers[1].(map[string]any)["image"]; got != "example.com/sidecar:v1" {
		t.Errorf("sidecar image = %q, want unchanged", got)
	}
}

func TestBuildSelfUpgradeApplyPatchRejectsMissingDeployment(t *testing.T) {
	_, err := buildSelfUpgradeApplyPatch("apiVersion: v1\nkind: Service\nmetadata:\n  name: kubeatlas\n", "kubeatlas", "kubeatlas", "kubeatlas", "ghcr.io/maomada/kubeatlas:1.6.2")
	if err == nil {
		t.Fatal("buildSelfUpgradeApplyPatch() error = nil, want missing deployment error")
	}
}

func nestedString(t *testing.T, obj map[string]any, fields ...string) string {
	t.Helper()
	var current any = obj
	for _, field := range fields {
		currentMap, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %q is not an object", fields, field)
		}
		current = currentMap[field]
	}
	value, ok := current.(string)
	if !ok {
		t.Fatalf("path %v: value is %T, want string", fields, current)
	}
	return value
}

func nestedFloat(t *testing.T, obj map[string]any, fields ...string) float64 {
	t.Helper()
	var current any = obj
	for _, field := range fields {
		currentMap, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %q is not an object", fields, field)
		}
		current = currentMap[field]
	}
	value, ok := current.(float64)
	if !ok {
		t.Fatalf("path %v: value is %T, want number", fields, current)
	}
	return value
}
