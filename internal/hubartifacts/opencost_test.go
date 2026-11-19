package hubartifacts

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionalOpenCostArtifactsArePinnedAndIsolated(t *testing.T) {
	dir, agentDigest, agentLayer := makeBundleForPlatforms(t, []string{"amd64"}, false, false)
	legacy := openTestCatalog(t, dir)
	if got := legacy.Availability(); got.OpenCostChartSHA256 != "" || got.OpenCostImageDigest != "" {
		t.Fatalf("legacy catalog advertised OpenCost: %+v", got)
	}
	checkArtifact(t, legacy, http.MethodGet, "/downloads/agent/1.2.3/opencost.tgz", http.StatusNotFound)
	_ = legacy.Close()

	layout, imageDigest, layerDigest := sideImageFixture(t)
	chart := writeOpenCostFixture(t, dir, "opencost")
	if err := copyTree(layout, filepath.Join(dir, openCostOCIPath)); err != nil {
		t.Fatal(err)
	}
	setOpenCostFixture(t, dir, digestHex(chart), imageDigest)
	catalog := openTestCatalog(t, dir)
	if got := catalog.Availability(); got.OpenCostChartSHA256 != digestHex(chart) || got.OpenCostImageDigest != imageDigest {
		t.Fatalf("catalog metadata does not pin both artifacts: %+v", got)
	}
	response := checkArtifact(t, catalog, http.MethodGet, "/downloads/agent/1.2.3/opencost.tgz", http.StatusOK)
	if !bytes.Equal(chart, response.Body.Bytes()) {
		t.Fatal("OpenCost chart download differs from the pinned archive")
	}
	response = checkArtifact(t, catalog, http.MethodGet, "/downloads/agent/1.2.3/checksums.txt", http.StatusOK)
	if !strings.Contains(response.Body.String(), digestHex(chart)+"  opencost.tgz\n") {
		t.Fatal("OpenCost chart checksum is missing")
	}
	response = checkArtifact(t, catalog, http.MethodHead, "/v2/kubeatlas/opencost/manifests/"+imageDigest, http.StatusOK)
	if response.Body.Len() != 0 || response.Header().Get("Docker-Content-Digest") != imageDigest {
		t.Fatal("OpenCost manifest HEAD is missing its digest or returned a body")
	}
	checkArtifact(t, catalog, http.MethodGet, "/v2/kubeatlas/opencost/blobs/"+layerDigest, http.StatusOK)
	checkArtifact(t, catalog, http.MethodGet, "/v2/kubeatlas/opencost/manifests/latest", http.StatusNotFound)
	checkArtifact(t, catalog, http.MethodPut, "/v2/kubeatlas/opencost/manifests/"+imageDigest, http.StatusMethodNotAllowed)
	checkArtifact(t, catalog, http.MethodGet, "/v2/kubeatlas/opencost/manifests/"+agentDigest, http.StatusNotFound)
	checkArtifact(t, catalog, http.MethodGet, "/v2/kubeatlas/opencost/blobs/"+agentLayer, http.StatusNotFound)
	checkArtifact(t, catalog, http.MethodGet, "/v2/kubeatlas/agent/manifests/"+imageDigest, http.StatusNotFound)
	checkArtifact(t, catalog, http.MethodGet, "/v2/kubeatlas/agent/blobs/"+layerDigest, http.StatusNotFound)
	checkArtifact(t, catalog, http.MethodGet, "/downloads/agent/9.9.9/opencost.tgz", http.StatusNotFound)
}

func TestOpenRejectsUnverifiedOpenCost(t *testing.T) {
	for _, tc := range []struct {
		name         string
		chartName    string
		wrongHash    bool
		corruptLayer bool
		missingArc   bool
	}{
		{name: "chart hash", chartName: "opencost", wrongHash: true},
		{name: "chart identity", chartName: "other"},
		{name: "image layers", chartName: "opencost", corruptLayer: true},
		{name: "platform coverage", chartName: "opencost", missingArc: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arch := []string{"amd64"}
			if tc.missingArc {
				arch = append(arch, "arm64")
			}
			dir, _, _ := makeBundleForPlatforms(t, arch, false, false)
			layout, imageDigest, layerDigest := sideImageFixture(t)
			if tc.corruptLayer {
				path := filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(layerDigest, "sha256:"))
				writeFixtureFile(t, path, []byte("COST-EXTRA-LAYER"))
			}
			if err := copyTree(layout, filepath.Join(dir, openCostOCIPath)); err != nil {
				t.Fatal(err)
			}
			chart := writeOpenCostFixture(t, dir, tc.chartName)
			chartHash := digestHex(chart)
			if tc.wrongHash {
				chartHash = strings.Repeat("0", 64)
			}
			setOpenCostFixture(t, dir, chartHash, imageDigest)
			if catalog, err := Open(dir, "1.2.3"); err == nil {
				_ = catalog.Close()
				t.Fatal("invalid OpenCost artifacts were accepted")
			}
		})
	}
}

func TestPreviousCatalogOnlyServesOCI(t *testing.T) {
	currentDir, currentDigest, _ := makeBundleForPlatforms(t, []string{"amd64"}, true, false)
	oldDir, oldDigest, _ := makeBundleForPlatforms(t, []string{"amd64"}, false, false)
	oldCostLayout, oldCostDigest, oldCostLayer := sideImageFixture(t)
	chart := writeOpenCostFixture(t, oldDir, "opencost")
	if err := copyTree(oldCostLayout, filepath.Join(oldDir, openCostOCIPath)); err != nil {
		t.Fatal(err)
	}
	setOpenCostFixture(t, oldDir, digestHex(chart), oldCostDigest)
	current := openTestCatalog(t, currentDir)
	previous := openTestCatalog(t, oldDir)
	checkArtifact(t, current, http.MethodGet, "/v2/kubeatlas/agent/manifests/"+oldDigest, http.StatusNotFound)
	current.WithPreviousImages(previous)
	checkArtifact(t, current, http.MethodGet, "/v2/kubeatlas/agent/manifests/"+currentDigest, http.StatusOK)
	checkArtifact(t, current, http.MethodHead, "/v2/kubeatlas/agent/manifests/"+oldDigest, http.StatusOK)
	checkArtifact(t, current, http.MethodGet, "/v2/kubeatlas/opencost/manifests/"+oldCostDigest, http.StatusOK)
	checkArtifact(t, current, http.MethodGet, "/v2/kubeatlas/opencost/blobs/"+oldCostLayer, http.StatusOK)
	checkArtifact(t, current, http.MethodGet, "/v2/kubeatlas/agent/manifests/"+oldCostDigest, http.StatusNotFound)
	checkArtifact(t, current, http.MethodGet, "/downloads/agent/1.2.3/opencost.tgz", http.StatusNotFound)
	if current.Availability().ImageDigest != currentDigest || current.Availability().OpenCostChartSHA256 != "" {
		t.Fatal("previous catalog supplied the active enrollment digest")
	}
}

func openTestCatalog(t *testing.T, dir string) *Catalog {
	t.Helper()
	catalog, err := Open(dir, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close() })
	return catalog
}

func checkArtifact(t *testing.T, catalog *Catalog, method, path string, status int) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	if !catalog.ServeHTTP(response, httptest.NewRequest(method, path, nil)) || response.Code != status {
		t.Fatalf("%s %s status = %d, want %d", method, path, response.Code, status)
	}
	return response
}

func writeOpenCostFixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	var contents bytes.Buffer
	archive := tar.NewWriter(&contents)
	metadata := []byte("apiVersion: v2\nname: " + name + "\nversion: 2.5.34\nappVersion: 1.121.4\n")
	writeChartTestEntry(t, archive, name+"/Chart.yaml", metadata)
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	chart := gzipChartTestArchive(t, contents.Bytes())
	writeFixtureFile(t, filepath.Join(dir, openCostChartName), chart)
	return chart
}

func setOpenCostFixture(t *testing.T, dir, chartHash, digest string) {
	t.Helper()
	path := filepath.Join(dir, "manifest.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.OpenCost = &OpenCostManifest{ChartSHA256: chartHash, ImageDigest: digest}
	contents, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, path, contents)
}

func sideImageFixture(t *testing.T) (layout, imageDigest, layerDigest string) {
	t.Helper()
	layout = t.TempDir()
	blobs := filepath.Join(layout, "blobs", "sha256")
	if err := os.MkdirAll(blobs, 0o700); err != nil {
		t.Fatal(err)
	}
	put := func(mediaType string, data []byte) descriptor {
		digest := digestBytes(data)
		writeFixtureFile(t, filepath.Join(blobs, strings.TrimPrefix(digest, "sha256:")), data)
		return descriptor{MediaType: mediaType, Digest: digest, Size: int64(len(data))}
	}
	config := put(ociConfigMedia, []byte(`{"os":"linux","architecture":"amd64"}`))
	layer := put("application/vnd.oci.image.layer.v1.tar+gzip", []byte("cost-extra-layer"))
	imageBytes, err := json.Marshal(imageDocument{
		SchemaVersion: 2, MediaType: ociImageMedia, Config: config, Layers: []descriptor{layer},
	})
	if err != nil {
		t.Fatal(err)
	}
	image := put(ociImageMedia, imageBytes)
	index, err := json.Marshal(indexDocument{SchemaVersion: 2, MediaType: ociIndexMedia, Manifests: []descriptor{image}})
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(layout, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`))
	writeFixtureFile(t, filepath.Join(layout, "index.json"), index)
	return layout, image.Digest, layer.Digest
}

func writeFixtureFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}
