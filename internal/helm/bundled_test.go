package helm

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/maomada/KubeAtlas/internal/bundledcharts"
	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/client-go/rest"
)

func TestBundledChartDetailWithoutRepositoryConfig(t *testing.T) {
	client := &Client{}
	detail, err := client.GetChartDetail(bundledcharts.Repository, "caretta", bundledcharts.CarettaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Version != bundledcharts.CarettaVersion || detail.Values["image"] == nil {
		t.Fatalf("missing embedded metadata/defaults: %+v", detail)
	}
	if _, err := client.GetChartDetail(bundledcharts.Repository, "caretta", "9.9.9"); err == nil {
		t.Fatal("unknown bundle version must not fall back to a remote repository")
	}
}

func TestBundledInstallPreservesPreflight(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		cfg := memoryActionConfig(t)
		seedRelease(t, cfg, "caretta", release.StatusPendingInstall, 1)
		client := &Client{}
		req := &InstallRequest{ReleaseName: "caretta", Namespace: "default", Repository: bundledcharts.Repository, ChartName: "caretta", Version: bundledcharts.CarettaVersion}
		var err error
		if streaming {
			_, err = client.installWithProgressUsing(cfg, req, make(chan InstallProgress, 16))
		} else {
			_, err = client.installWith(cfg, req)
		}
		var pending *ReleasePendingError
		if !errors.As(err, &pending) {
			t.Fatalf("streaming=%t: expected pending-release refusal, got %v", streaming, err)
		}
	}
}

func TestBundledInstallUnknownVersionWithoutNetwork(t *testing.T) {
	client := &Client{}
	for _, streaming := range []bool{false, true} {
		req := &InstallRequest{ReleaseName: "caretta", Namespace: "default", Repository: bundledcharts.Repository, ChartName: "caretta", Version: "9.9.9"}
		var err error
		if streaming {
			_, err = client.installWithProgressUsing(memoryActionConfig(t), req, make(chan InstallProgress, 16))
		} else {
			_, err = client.installWith(memoryActionConfig(t), req)
		}
		if err == nil {
			t.Fatal("accepted unbundled version")
		}
	}
}

func TestBundledInstallWithoutRepositoryConfig(t *testing.T) {
	client := &Client{}
	for _, streaming := range []bool{false, true} {
		cfg := memoryActionConfig(t)
		req := &InstallRequest{ReleaseName: "caretta", Namespace: "default", Repository: bundledcharts.Repository, ChartName: "caretta", Version: bundledcharts.CarettaVersion}
		progress := make(chan InstallProgress, 16)
		var installed *HelmRelease
		var err error
		if streaming {
			installed, err = client.installWithProgressUsing(cfg, req, progress)
		} else {
			installed, err = client.installWith(cfg, req)
		}
		if err != nil {
			t.Fatal(err)
		}
		if installed.Name != req.ReleaseName {
			t.Fatalf("wrong release: %+v", installed)
		}
		stored, err := cfg.Releases.Last(req.ReleaseName)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Chart.Metadata.Version != bundledcharts.CarettaVersion {
			t.Fatal("release did not store the pinned chart")
		}
		if source, ok := confirmedChartSource(stored); !ok || source.URL != "https://helm.groundcover.com" {
			t.Fatalf("missing upstream provenance: %+v", source)
		}
		if streaming && len(progress) == 0 {
			t.Fatal("missing local install progress")
		}
	}
}

func TestOperatorOpenCostDetailAndFreshInstall(t *testing.T) {
	const registry = "registry.example.test"
	const version = "1.2.3"
	const name = bundledcharts.OpenCostName
	image := registry + "/opencost@sha256:" + strings.Repeat("a", 64)
	path, err := chartutil.Save(&chart.Chart{
		Metadata:  &chart.Metadata{APIVersion: "v2", Name: name, Version: version},
		Values:    map[string]any{"fixtureSecret": "must-not-be-shared"},
		Templates: []*chart.File{{Name: "templates/pod.yaml", Data: []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: opencost\nspec:\n  containers:\n    - name: opencost\n      image: " + image + "\n")}},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBEATLAS_OPENCOST_CHART_PATH", path)
	t.Setenv("KUBEATLAS_OPENCOST_CHART_SHA256", fmt.Sprintf("%x", sha256.Sum256(data)))
	t.Setenv("KUBEATLAS_OPENCOST_IMAGE_REGISTRY", registry)

	client := &Client{}
	detail, err := client.GetChartDetail(bundledcharts.Repository, name, "")
	if err != nil || detail.Version != version || detail.Values != nil {
		t.Fatalf("missing exact chart metadata or leaked operator values: %+v, %v", detail, err)
	}
	request := InstallRequest{ReleaseName: name, Namespace: name, Repository: bundledcharts.Repository, ChartName: name, Version: version}
	var alreadyInstalled atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Impersonate-User") != "fixture-user" ||
			r.URL.Query().Get("labelSelector") != "app.kubernetes.io/name in (opencost,prometheus-opencost-exporter)" {
			t.Errorf("inventory probe did not use the caller and fixed selector: %s %s", r.Method, r.URL.Path)
			http.Error(w, "wrong caller or selector", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/apis/apps/v1/deployments":
			if alreadyInstalled.Load() {
				fmt.Fprint(w, "{\"apiVersion\":\"apps/v1\",\"kind\":\"DeploymentList\",\"items\":[{\"metadata\":{\"name\":\"opencost\",\"namespace\":\"monitoring\"}}]}")
			} else {
				fmt.Fprint(w, "{\"apiVersion\":\"apps/v1\",\"kind\":\"DeploymentList\",\"items\":[]}")
			}
		case "/api/v1/services":
			fmt.Fprint(w, "{\"apiVersion\":\"v1\",\"kind\":\"ServiceList\",\"items\":[]}")
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	for _, streaming := range []bool{false, true} {
		cfg := memoryActionConfig(t)
		cfg.Releases.Driver.(*driver.Memory).SetNamespace(name)
		cfg.RESTClientGetter = newRESTConfigGetter(&rest.Config{Host: api.URL}, name, &pkgauth.User{Username: "fixture-user"})
		install := func(request *InstallRequest) (*HelmRelease, error) {
			if streaming {
				return client.installWithProgressUsing(cfg, request, make(chan InstallProgress, 16))
			}
			return client.installWith(cfg, request)
		}
		for _, invalid := range []InstallRequest{
			{ReleaseName: "different", Namespace: name, Repository: bundledcharts.Repository, ChartName: name, Version: version},
			{ReleaseName: name, Namespace: "different", Repository: bundledcharts.Repository, ChartName: name, Version: version},
			{ReleaseName: name, Namespace: name, Repository: bundledcharts.Repository, ChartName: name, Version: version, Values: map[string]any{}},
		} {
			if _, err := install(&invalid); classifyInstallError(err).Status != http.StatusBadRequest {
				t.Fatalf("streaming=%t: accepted invalid install: %v", streaming, err)
			}
		}
		err = cfg.Releases.Create(&release.Release{Name: name, Namespace: name, Version: 1, Info: &release.Info{Status: release.StatusFailed}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := install(&request); classifyInstallError(err).Code != "release_history" {
			t.Fatalf("streaming=%t: failed history was not rejected: %v", streaming, err)
		}
		if _, err := cfg.Releases.Delete(name, 1); err != nil {
			t.Fatal(err)
		}
		alreadyInstalled.Store(true)
		if _, err := install(&request); classifyInstallError(err).Code != "opencost_exists" {
			t.Fatalf("streaming=%t: existing installation in another namespace was not rejected: %v", streaming, err)
		}
		alreadyInstalled.Store(false)
		installed, err := install(&request)
		if err != nil || installed.Name != name || installed.Namespace != name {
			t.Fatalf("streaming=%t: fresh install failed: %+v, %v", streaming, installed, err)
		}
		if _, err := install(&request); classifyInstallError(err).Status != http.StatusConflict {
			t.Fatalf("streaming=%t: subsequent install did not refuse: %v", streaming, err)
		}
	}
}
