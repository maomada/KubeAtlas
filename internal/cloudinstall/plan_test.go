package cloudinstall

import (
	"errors"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/internal/helm"
	"github.com/maomada/KubeAtlas/pkg/subject"
)

type fakeReleaseInspector struct {
	inspection helm.CloudReleaseInspection
	err        error
	calls      int
}

func (f *fakeReleaseInspector) InspectCloudRelease(_, _ string) (helm.CloudReleaseInspection, error) {
	f.calls++
	return f.inspection, f.err
}

func nativeKubeAtlasTarget(namespace, release string) KubeAtlasTarget {
	ref := subject.Ref{Kind: "HelmRelease", Namespace: namespace, Name: release}
	return KubeAtlasTarget{
		Namespace: namespace, ReleaseName: release, DeploymentName: release,
		Chart: "kubeatlas-1.5.4",
		Ownership: TargetOwnership{
			Classification: OwnershipNativeHelm,
			NativeHelm:     &ref, NativeHelmMatchesTarget: true,
		},
	}
}

func TestClassifyInstallPlanFreshAdoptAndGitOps(t *testing.T) {
	t.Run("fresh exact target", func(t *testing.T) {
		releases := &fakeReleaseInspector{inspection: helm.CloudReleaseInspection{State: helm.CloudReleaseNone}}
		plan, err := ClassifyInstallPlan(DiscoveryResult{}, releases, "kubeatlas", "kubeatlas", true)
		if err != nil || plan.Mode != InstallModeFresh || plan.Namespace != "kubeatlas" || plan.Release != "kubeatlas" {
			t.Fatalf("plan = %#v, err = %v", plan, err)
		}
	})

	t.Run("auto-discovery miss preserves requested fresh target", func(t *testing.T) {
		releases := &fakeReleaseInspector{inspection: helm.CloudReleaseInspection{State: helm.CloudReleaseNone}}
		plan, err := ClassifyInstallPlan(DiscoveryResult{}, releases, "observability", "prod", false)
		if err != nil || plan.Mode != InstallModeFresh || plan.Namespace != "observability" || plan.Release != "prod" {
			t.Fatalf("plan = %#v, err = %v", plan, err)
		}
	})

	t.Run("auto-select native Helm", func(t *testing.T) {
		target := nativeKubeAtlasTarget("observability", "prod")
		releases := &fakeReleaseInspector{inspection: helm.CloudReleaseInspection{State: helm.CloudReleaseDeployed, Revision: 3}}
		plan, err := ClassifyInstallPlan(DiscoveryResult{ClusterWide: []KubeAtlasTarget{target}}, releases, "kubeatlas", "kubeatlas", false)
		if err != nil || plan.Mode != InstallModeAdopt || plan.Namespace != "observability" || plan.Release != "prod" {
			t.Fatalf("plan = %#v, err = %v", plan, err)
		}
	})

	t.Run("verified GitOps bypasses Helm storage", func(t *testing.T) {
		target := nativeKubeAtlasTarget("observability", "prod")
		target.Ownership = TargetOwnership{
			Classification: OwnershipGitOpsVerified,
			Controllers: []ControllerCandidate{{
				Ref:          subject.Ref{Group: "argoproj.io", Kind: "Application", Namespace: "argocd", Name: "kubeatlas"},
				Verification: ControllerVerified,
			}},
		}
		releases := &fakeReleaseInspector{err: errors.New("should not be called")}
		plan, err := ClassifyInstallPlan(DiscoveryResult{Namespace: []KubeAtlasTarget{target}}, releases, "kubeatlas", "kubeatlas", false)
		if err != nil || plan.Mode != InstallModeGitOps || releases.calls != 0 {
			t.Fatalf("plan = %#v, calls = %d, err = %v", plan, releases.calls, err)
		}
	})
}

func TestClassifyInstallPlanFailsClosed(t *testing.T) {
	t.Run("multiple auto-discovered targets", func(t *testing.T) {
		result := DiscoveryResult{Namespace: []KubeAtlasTarget{
			nativeKubeAtlasTarget("kubeatlas", "one"), nativeKubeAtlasTarget("kubeatlas", "two"),
		}}
		_, err := ClassifyInstallPlan(result, &fakeReleaseInspector{}, "kubeatlas", "kubeatlas", false)
		var multiple *MultipleTargetsError
		if !errors.As(err, &multiple) || len(multiple.Targets) != 2 {
			t.Fatalf("error = %v", err)
		}
		if !strings.Contains(err.Error(), "multiple KubeAtlas installations") || !strings.Contains(err.Error(), `release "one"`) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("unmanaged workload collision", func(t *testing.T) {
		target := nativeKubeAtlasTarget("kubeatlas", "kubeatlas")
		target.Ownership = TargetOwnership{Classification: OwnershipGeneric}
		_, err := ClassifyInstallPlan(
			DiscoveryResult{Selected: []KubeAtlasTarget{target}},
			&fakeReleaseInspector{inspection: helm.CloudReleaseInspection{State: helm.CloudReleaseNone}},
			"kubeatlas", "kubeatlas", true,
		)
		if err == nil || !strings.Contains(err.Error(), "unmanaged installation") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("uncertain GitOps ownership", func(t *testing.T) {
		target := nativeKubeAtlasTarget("kubeatlas", "kubeatlas")
		target.Ownership.Classification = OwnershipGitOpsUnreadable
		_, err := ClassifyInstallPlan(
			DiscoveryResult{Selected: []KubeAtlasTarget{target}},
			&fakeReleaseInspector{}, "kubeatlas", "kubeatlas", true,
		)
		if err == nil || !strings.Contains(err.Error(), "refusing an imperative upgrade") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("pending Helm release", func(t *testing.T) {
		_, err := ClassifyInstallPlan(
			DiscoveryResult{},
			&fakeReleaseInspector{inspection: helm.CloudReleaseInspection{
				State: helm.CloudReleasePending, Status: "pending-upgrade", Revision: 4,
			}},
			"kubeatlas", "kubeatlas", true,
		)
		if err == nil || !strings.Contains(err.Error(), "pending-upgrade") || !strings.Contains(err.Error(), "revision 4") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestClassifyInstallPlanCarriesClusterWideUncertainty(t *testing.T) {
	scanErr := errors.New("cluster-wide list forbidden")
	plan, err := ClassifyInstallPlan(
		DiscoveryResult{ClusterWideError: scanErr},
		&fakeReleaseInspector{inspection: helm.CloudReleaseInspection{State: helm.CloudReleaseNone}},
		"kubeatlas", "kubeatlas", false,
	)
	if err != nil || !errors.Is(plan.ClusterWideScanError, scanErr) {
		t.Fatalf("plan = %#v, err = %v", plan, err)
	}
}

func TestClassifyInstallPlanReleaseInspectErrorCarriesTheDiscoveredTarget(t *testing.T) {
	var e *ReleaseInspectError
	err := error(&ReleaseInspectError{Namespace: "monitoring", Release: "kubeatlas-prod", Existing: true, Err: errors.New("secrets is forbidden")})
	if !errors.As(err, &e) || !e.Existing || e.Release != "kubeatlas-prod" {
		t.Fatalf("typed error lost its target: %+v", e)
	}
	if got := err.Error(); got != `inspect Helm release "kubeatlas-prod" in namespace "monitoring": secrets is forbidden` {
		t.Fatalf("message = %q", got)
	}
}
