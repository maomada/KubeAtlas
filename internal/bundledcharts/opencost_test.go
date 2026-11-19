package bundledcharts

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
)

const testRegistry = "registry.example.test:5443"

func stageOpenCost(t *testing.T, image string, dependency bool) {
	t.Helper()
	template := []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: opencost\nspec:\n  containers:\n    - name: opencost\n      image: " + image + "\n")
	prepared := &chart.Chart{
		Metadata:  &chart.Metadata{APIVersion: "v2", Name: OpenCostName, Version: "1.2.3"},
		Templates: []*chart.File{{Name: "templates/pod.yaml", Data: template}},
	}
	if dependency {
		prepared.Metadata.Dependencies = []*chart.Dependency{{Name: "unavailable", Version: "1.0.0", Repository: "https://fixture.invalid"}}
	}
	path, err := chartutil.Save(prepared, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBEATLAS_OPENCOST_CHART_PATH", path)
	t.Setenv("KUBEATLAS_OPENCOST_CHART_SHA256", fmt.Sprintf("%x", sha256.Sum256(data)))
	t.Setenv("KUBEATLAS_OPENCOST_IMAGE_REGISTRY", testRegistry)
}

func TestLoadOperatorOpenCost(t *testing.T) {
	image := testRegistry + "/observability/opencost@sha256:" + strings.Repeat("a", 64)
	stageOpenCost(t, image, false)
	loaded, source, err := Load(OpenCostName, "")
	if err != nil || loaded.Name() != OpenCostName || source.Version != "1.2.3" || source.ImageRegistry != testRegistry {
		t.Fatalf("operator chart resolution failed: %+v, %v", source, err)
	}
	if _, _, err := Load(OpenCostName, "latest"); !errors.Is(err, ErrOpenCostUnavailable) {
		t.Fatalf("unpinned version unexpectedly accepted: %v", err)
	}
	t.Setenv("KUBEATLAS_OPENCOST_CHART_SHA256", strings.Repeat("0", 64))
	if _, _, err := Load(OpenCostName, "1.2.3"); !errors.Is(err, ErrOpenCostUnavailable) {
		t.Fatalf("digest mismatch unexpectedly accepted: %v", err)
	}
}

func TestOperatorOpenCostRejectsUnpreparedArchives(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("a", 64)
	for _, variant := range []struct {
		name, image string
		dependency  bool
	}{
		{name: "wrong registry", image: "registry.example.test/observability/opencost" + digest},
		{name: "image tag", image: testRegistry + "/observability/opencost:latest"},
		{name: "missing dependency", image: testRegistry + "/observability/opencost" + digest, dependency: true},
	} {
		t.Run(variant.name, func(t *testing.T) {
			stageOpenCost(t, variant.image, variant.dependency)
			if _, _, err := Load(OpenCostName, "1.2.3"); !errors.Is(err, ErrOpenCostUnavailable) {
				t.Fatalf("unprepared chart unexpectedly accepted: %v", err)
			}
		})
	}
}
