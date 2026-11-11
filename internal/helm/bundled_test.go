package helm

import (
	"errors"
	"testing"

	"github.com/maomada/KubeAtlas/internal/bundledcharts"
	"helm.sh/helm/v3/pkg/release"
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
