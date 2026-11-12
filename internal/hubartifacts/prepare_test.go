package hubartifacts

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func privateOutputPath(t *testing.T, name string) string {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parent, name)
}

func cleanupReadOnlyBundle(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(func() {
		_ = filepath.WalkDir(path, func(current string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			mode := os.FileMode(0o600)
			if entry.IsDir() {
				mode = 0o700
			}
			return os.Chmod(current, mode)
		})
	})
}

func TestPreparePackagesUnchangedSourceChartWithBuildAndChartVersions(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("Helm is required for the source-chart preparation regression")
	}
	fixture, imageDigest, _ := makeBundle(t)
	output := privateOutputPath(t, "prepared")
	cleanupReadOnlyBundle(t, output)
	err = Prepare(PrepareOptions{
		ChartDir:      filepath.Join("..", "..", "deploy", "helm", "kubeatlas"),
		LayoutDir:     filepath.Join(fixture, "oci"),
		InstallerPath: filepath.Join("..", "..", "scripts", "install-hub-agent.sh"),
		OutputDir:     output,
		Version:       "1.2.3",
		Revision:      testRevision,
		ImageDigest:   imageDigest,
		HelmBinary:    helm,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Open(output, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close() })
	if catalog.manifest.Chart.Version != "0.0.0-dev."+testRevision[:12] || catalog.manifest.Version != "1.2.3" {
		t.Fatalf("prepared catalog versions = build %q, chart %q", catalog.manifest.Version, catalog.manifest.Chart.Version)
	}
}

func TestPreparePackagesValidReleaseChartVersion(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("Helm is required for the source-chart preparation regression")
	}
	fixture, imageDigest, _ := makeBundle(t)
	output := privateOutputPath(t, "prepared-release")
	cleanupReadOnlyBundle(t, output)
	const chartVersion = "1.2.3+build.7"
	err = Prepare(PrepareOptions{
		ChartDir:      filepath.Join("..", "..", "deploy", "helm", "kubeatlas"),
		LayoutDir:     filepath.Join(fixture, "oci"),
		InstallerPath: filepath.Join("..", "..", "scripts", "install-hub-agent.sh"),
		OutputDir:     output,
		Version:       "1.2.3",
		ChartVersion:  chartVersion,
		Revision:      testRevision,
		ImageDigest:   imageDigest,
		HelmBinary:    helm,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Open(output, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close() })
	if catalog.manifest.Chart.Version != chartVersion {
		t.Fatalf("prepared chart version = %q, want %q", catalog.manifest.Chart.Version, chartVersion)
	}
}
