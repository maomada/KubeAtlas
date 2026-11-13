package hubartifacts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"
const testChartVersion = "0.0.0-dev.0123456789ab"

func TestOpenAndReadOnlyArtifactRoutes(t *testing.T) {
	dir, digest, layerDigest := makeBundle(t)
	catalog, err := Open(dir, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close() })
	if got := catalog.Availability(); !got.Available || got.BootstrapAvailable || got.ImageDigest != digest || got.ChartSHA256 == "" || got.ChartVersion != testChartVersion || len(got.Platforms) != 2 {
		t.Fatalf("unexpected availability: %+v", got)
	}
	tests := []struct {
		method, path, rangeHeader string
		status                    int
	}{
		{http.MethodGet, "/v2/", "", http.StatusOK},
		{http.MethodGet, "/v2/kubeatlas/agent/manifests/" + digest, "", http.StatusOK},
		{http.MethodHead, "/v2/kubeatlas/agent/blobs/" + layerDigest, "", http.StatusOK},
		{http.MethodGet, "/v2/kubeatlas/agent/blobs/" + layerDigest, "bytes=1-3", http.StatusPartialContent},
		{http.MethodGet, "/downloads/agent/1.2.3/chart.tgz", "", http.StatusOK},
		{http.MethodGet, "/downloads/agent/1.2.3/image-manifest.json", "", http.StatusOK},
		{http.MethodGet, "/downloads/agent/1.2.3/checksums.txt", "", http.StatusOK},
		{http.MethodGet, "/v2/kubeatlas/agent/manifests/1.2.3", "", http.StatusNotFound},
		{http.MethodPut, "/v2/kubeatlas/agent/manifests/" + digest, "", http.StatusMethodNotAllowed},
		{http.MethodGet, "/v2/kubeatlas/other/manifests/" + digest, "", http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.method+test.path, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.path, nil)
			if test.rangeHeader != "" {
				req.Header.Set("Range", test.rangeHeader)
			}
			res := httptest.NewRecorder()
			handled := catalog.ServeHTTP(res, req)
			if !handled || res.Code != test.status {
				t.Fatalf("handled=%t status=%d want=%d body=%s", handled, res.Code, test.status, res.Body.String())
			}
			if test.method == http.MethodHead && test.status == http.StatusOK && res.Header().Get("Docker-Content-Digest") != layerDigest {
				t.Fatalf("missing digest header: %v", res.Header())
			}
			if test.rangeHeader != "" && res.Header().Get("Content-Range") == "" {
				t.Fatalf("missing Content-Range: %v", res.Header())
			}
		})
	}
}

func TestBootstrapArtifactIsOptionalAndHashPinned(t *testing.T) {
	dir, _, _ := makeBundle(t)
	catalog, err := Open(dir, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Availability().BootstrapAvailable {
		t.Fatal("legacy bundle advertised a bootstrap artifact")
	}
	response := httptest.NewRecorder()
	catalog.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/downloads/agent/1.2.3/bootstrap.sh", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing bootstrap status = %d", response.Code)
	}
	_ = catalog.Close()

	bootstrap := []byte("#!/usr/bin/env bash\\nexit 0\\n")
	if err := os.WriteFile(filepath.Join(dir, "bootstrap-hub-agent.sh"), bootstrap, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Bootstrap = &struct {
		File   string `json:"file"`
		SHA256 string `json:"sha256"`
	}{File: "bootstrap-hub-agent.sh", SHA256: digestHex(bootstrap)}
	manifestBytes, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err = Open(dir, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if !catalog.Availability().BootstrapAvailable {
		t.Fatal("valid bootstrap artifact was not advertised")
	}
	response = httptest.NewRecorder()
	catalog.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/downloads/agent/1.2.3/bootstrap.sh", nil))
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), bootstrap) {
		t.Fatalf("bootstrap response status=%d body=%q", response.Code, response.Body.String())
	}
	_ = catalog.Close()
	if err := os.WriteFile(filepath.Join(dir, "bootstrap-hub-agent.sh"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, "1.2.3"); err == nil {
		t.Fatal("bootstrap hash mismatch was accepted")
	}
}

func TestOpenSupportsSingleImagePlatforms(t *testing.T) {
	for _, architecture := range []string{"amd64", "arm64"} {
		t.Run(architecture, func(t *testing.T) {
			dir, _, _ := makeBundleForPlatforms(t, []string{architecture}, true, false)
			catalog, err := Open(dir, "1.2.3")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = catalog.Close() })
			got := catalog.Availability().Platforms
			if len(got) != 1 || got[0] != "linux/"+architecture {
				t.Fatalf("available platforms = %v", got)
			}
		})
	}
}

func TestOpenUsesImageConfigWhenIndexPlatformIsAbsent(t *testing.T) {
	dir, _, _ := makeBundleForPlatforms(t, []string{"arm64"}, false, true)
	catalog, err := Open(dir, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close() })
	if got := catalog.Availability().Platforms; len(got) != 1 || got[0] != "linux/arm64" {
		t.Fatalf("available platforms = %v", got)
	}
}

func TestOpenRejectsConflictingImagePlatformDescriptors(t *testing.T) {
	dir, _, _ := makeBundle(t)
	rewriteSelectedIndex(t, dir, func(index *indexDocument) {
		first := index.Manifests[0]
		second := first
		second.Platform = &struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		}{OS: "linux", Architecture: "arm64"}
		index.Manifests = []descriptor{first, second}
	})
	if _, err := Open(dir, "1.2.3"); err == nil {
		t.Fatal("expected conflicting descriptor platforms to be rejected")
	}
}

func TestOpenRejectsExcessiveDescriptorDepthAndCount(t *testing.T) {
	t.Run("depth", func(t *testing.T) {
		dir, _, _ := makeBundle(t)
		manifestBytes, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		var manifest Manifest
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			t.Fatal(err)
		}
		layout := filepath.Join(dir, "oci")
		current := manifest.Image.Digest
		for range maxGraphDepth {
			indexBytes, _ := json.Marshal(indexDocument{SchemaVersion: 2, MediaType: ociIndexMedia, Manifests: []descriptor{{MediaType: ociIndexMedia, Digest: current, Size: int64(len(readBlobForTest(t, layout, current)))}}})
			current = writeBlobForTest(t, layout, indexBytes)
		}
		setImageRoot(t, dir, current, ociIndexMedia, int64(len(readBlobForTest(t, layout, current))))
		if _, err := Open(dir, "1.2.3"); err == nil {
			t.Fatal("expected excessive descriptor depth to be rejected")
		}
	})
	t.Run("count", func(t *testing.T) {
		dir, _, _ := makeBundle(t)
		rootIndex := make([]descriptor, maxDescriptorsPerIndex+1)
		for i := range rootIndex {
			rootIndex[i] = descriptor{MediaType: ociIndexMedia, Digest: "sha256:" + string(bytes.Repeat([]byte{'0'}, 64)), Size: 1}
		}
		data, _ := json.Marshal(indexDocument{SchemaVersion: 2, MediaType: ociIndexMedia, Manifests: rootIndex})
		if err := os.WriteFile(filepath.Join(dir, "oci", "index.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir, "1.2.3"); err == nil {
			t.Fatal("expected excessive root descriptor count to be rejected")
		}
	})
}

func TestOpenRejectsCorruptReachableLayer(t *testing.T) {
	dir, _, layerDigest := makeBundle(t)
	path := filepath.Join(dir, "oci", "blobs", "sha256", layerDigest[len("sha256:"):])
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, "1.2.3"); err == nil {
		t.Fatal("expected corrupt layer to fail startup validation")
	}
}

func TestServeRejectsChangedEntriesWithoutLeakingBytes(t *testing.T) {
	tests := []struct {
		name string
		path func(string, string, string) string
		file func(*testing.T, *Catalog, string, string, string) string
	}{
		{name: "installer", path: func(_, _, _ string) string { return "/downloads/agent/1.2.3/installer.sh" }, file: func(_ *testing.T, _ *Catalog, dir, _, _ string) string {
			return filepath.Join(dir, "install-hub-agent.sh")
		}},
		{name: "chart", path: func(_, _, _ string) string { return "/downloads/agent/1.2.3/chart.tgz" }, file: func(_ *testing.T, _ *Catalog, dir, _, _ string) string {
			return filepath.Join(dir, "kubeatlas-"+testChartVersion+".tgz")
		}},
		{name: "catalog-manifest", path: func(_, _, _ string) string { return "/downloads/agent/1.2.3/manifest.json" }, file: func(_ *testing.T, _ *Catalog, dir, _, _ string) string { return filepath.Join(dir, "manifest.json") }},
		{name: "image-index", path: func(digest, _, _ string) string { return "/downloads/agent/1.2.3/image-manifest.json" }, file: func(_ *testing.T, _ *Catalog, dir, digest, _ string) string { return blobPath(dir, digest) }},
		{name: "image-config", path: func(_, configDigest, _ string) string { return "/v2/kubeatlas/agent/blobs/" + configDigest }, file: func(_ *testing.T, _ *Catalog, dir, configDigest, _ string) string { return blobPath(dir, configDigest) }},
		{name: "image-layer", path: func(_, _, layerDigest string) string { return "/v2/kubeatlas/agent/blobs/" + layerDigest }, file: func(_ *testing.T, _ *Catalog, dir, _, layerDigest string) string { return blobPath(dir, layerDigest) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir, imageDigest, layerDigest := makeBundle(t)
			catalog, err := Open(dir, "1.2.3")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = catalog.Close() })
			configDigest := configDigestForFixture(t, dir)
			file := test.file(t, catalog, dir, imageDigest, layerDigest)
			if test.name == "image-config" {
				file = test.file(t, catalog, dir, configDigest, layerDigest)
			}
			mutated := mutateSameSize(t, file)
			path := test.path(imageDigest, configDigest, layerDigest)
			response := httptest.NewRecorder()
			if !catalog.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil)) || response.Code != http.StatusNotFound {
				t.Fatalf("response status=%d body=%q", response.Code, response.Body.String())
			}
			if bytes.Contains(response.Body.Bytes(), mutated) || response.Header().Get("Docker-Content-Digest") != "" || response.Header().Get("Content-Disposition") != "" {
				t.Fatalf("changed artifact leaked or retained stale headers: headers=%v body=%q", response.Header(), response.Body.String())
			}
		})
	}
}

func TestServeRejectsTruncatedArtifactAndSymlinkSubstitution(t *testing.T) {
	t.Run("truncated", func(t *testing.T) {
		dir, _, _ := makeBundle(t)
		catalog, err := Open(dir, "1.2.3")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = catalog.Close() })
		path := filepath.Join(dir, "install-hub-agent.sh")
		if err := os.WriteFile(path, []byte("truncated"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertArtifactUnavailable(t, catalog, "/downloads/agent/1.2.3/installer.sh")
	})
	t.Run("file-symlink", func(t *testing.T) {
		dir, _, layerDigest := makeBundle(t)
		catalog, err := Open(dir, "1.2.3")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = catalog.Close() })
		path := blobPath(dir, layerDigest)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "private")
		if err := os.WriteFile(outside, []byte("private bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, path); err != nil {
			t.Fatal(err)
		}
		assertArtifactUnavailable(t, catalog, "/v2/kubeatlas/agent/blobs/"+layerDigest)
	})
	t.Run("ancestor-symlink", func(t *testing.T) {
		dir, _, layerDigest := makeBundle(t)
		catalog, err := Open(dir, "1.2.3")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = catalog.Close() })
		layout := filepath.Join(dir, "oci")
		blobs := filepath.Join(layout, "blobs")
		saved := filepath.Join(layout, "blobs.saved")
		if err := os.Rename(blobs, saved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(saved, blobs); err != nil {
			t.Fatal(err)
		}
		assertArtifactUnavailable(t, catalog, "/v2/kubeatlas/agent/blobs/"+layerDigest)
	})
	t.Run("root-symlink", func(t *testing.T) {
		fixture, _, _ := makeBundle(t)
		parent := t.TempDir()
		dir := filepath.Join(parent, "bundle")
		if err := os.Rename(fixture, dir); err != nil {
			t.Fatal(err)
		}
		catalog, err := Open(dir, "1.2.3")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = catalog.Close() })
		moved := filepath.Join(parent, "original")
		if err := os.Rename(dir, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, dir); err != nil {
			t.Fatal(err)
		}
		assertArtifactUnavailable(t, catalog, "/downloads/agent/1.2.3/installer.sh")
	})
	t.Run("root-ancestor-symlink", func(t *testing.T) {
		fixture, _, _ := makeBundle(t)
		parent := t.TempDir()
		ancestor := filepath.Join(parent, "bundle-parent")
		if err := os.Mkdir(ancestor, 0o700); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(ancestor, "bundle")
		if err := os.Rename(fixture, dir); err != nil {
			t.Fatal(err)
		}
		catalog, err := Open(dir, "1.2.3")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = catalog.Close() })
		moved := filepath.Join(parent, "original-parent")
		if err := os.Rename(ancestor, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, ancestor); err != nil {
			t.Fatal(err)
		}
		assertArtifactUnavailable(t, catalog, "/downloads/agent/1.2.3/installer.sh")
	})
}

func assertArtifactUnavailable(t *testing.T, catalog *Catalog, path string) {
	t.Helper()
	response := httptest.NewRecorder()
	if !catalog.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil)) || response.Code != http.StatusNotFound {
		t.Fatalf("response status=%d body=%q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private bytes") || response.Header().Get("Docker-Content-Digest") != "" {
		t.Fatalf("artifact bytes or stale digest leaked: headers=%v body=%q", response.Header(), response.Body.String())
	}
}

func blobPath(dir, digest string) string {
	return filepath.Join(dir, "oci", "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))
}

func configDigestForFixture(t *testing.T, dir string) string {
	t.Helper()
	manifestBytes, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	indexBytes := readBlobForTest(t, filepath.Join(dir, "oci"), manifest.Image.Digest)
	var index indexDocument
	if err := json.Unmarshal(indexBytes, &index); err != nil {
		t.Fatal(err)
	}
	imageBytes := readBlobForTest(t, filepath.Join(dir, "oci"), index.Manifests[0].Digest)
	var image imageDocument
	if err := json.Unmarshal(imageBytes, &image); err != nil {
		t.Fatal(err)
	}
	return image.Config.Digest
}

func mutateSameSize(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		t.Fatalf("read artifact: %v", err)
	}
	data[0] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	changed := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestOpenAllowsMissingCatalogOnlyWhenUnconfigured(t *testing.T) {
	if catalog, err := Open("", ""); err != nil || catalog != nil {
		t.Fatalf("unconfigured catalog = %v, %v", catalog, err)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "missing"), "1.2.3"); err == nil {
		t.Fatal("configured missing catalog must fail")
	}
}

func makeBundle(t *testing.T) (string, string, string) {
	return makeBundleForPlatforms(t, []string{"amd64", "arm64"}, false, false)
}

func makeBundleForPlatforms(t *testing.T, architectures []string, singleImage, omitPlatforms bool) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	chart := chartArchive(t)
	installer := []byte("#!/usr/bin/env bash\nexit 0\n")
	if err := os.WriteFile(filepath.Join(dir, "kubeatlas-"+testChartVersion+".tgz"), chart, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install-hub-agent.sh"), installer, 0o600); err != nil {
		t.Fatal(err)
	}
	layout := filepath.Join(dir, "oci")
	blobs := filepath.Join(layout, "blobs", "sha256")
	if err := os.MkdirAll(blobs, 0o700); err != nil {
		t.Fatal(err)
	}
	put := func(data []byte) string {
		digest := digestBytes(data)
		if err := os.WriteFile(filepath.Join(blobs, digest[len("sha256:"):]), data, 0o600); err != nil {
			t.Fatal(err)
		}
		return digest
	}
	layer := []byte("test-layer-content")
	layerDigest := put(layer)
	manifests := make([]map[string]any, 0, len(architectures))
	imageDescriptors := make([]descriptor, 0, len(architectures))
	for _, architecture := range architectures {
		config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": architecture, "config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.version": "1.2.3", "org.opencontainers.image.revision": testRevision}}})
		configDigest := put(config)
		manifest, _ := json.Marshal(map[string]any{
			"schemaVersion": 2, "mediaType": ociImageMedia,
			"config": map[string]any{"mediaType": ociConfigMedia, "digest": configDigest, "size": len(config)},
			"layers": []any{map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": layerDigest, "size": len(layer)}},
		})
		manifestDigest := put(manifest)
		manifestDescriptor := map[string]any{
			"mediaType": ociImageMedia, "digest": manifestDigest, "size": len(manifest),
		}
		if !omitPlatforms {
			manifestDescriptor["platform"] = map[string]string{"os": "linux", "architecture": architecture}
		}
		manifests = append(manifests, manifestDescriptor)
		imageDescriptors = append(imageDescriptors, descriptor{MediaType: ociImageMedia, Digest: manifestDigest, Size: int64(len(manifest)), Platform: &struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		}{OS: "linux", Architecture: architecture}})
	}
	indexDigest := ""
	rootMedia := ociIndexMedia
	rootSize := 0
	if singleImage {
		if len(manifests) != 1 {
			t.Fatal("single-image fixture requires one architecture")
		}
		rootMedia = ociImageMedia
		rootSize = int(imageDescriptors[0].Size)
		indexDigest = imageDescriptors[0].Digest
	} else {
		index, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": ociIndexMedia, "manifests": manifests})
		indexDigest = put(index)
		rootSize = len(index)
	}
	if err := os.WriteFile(filepath.Join(layout, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rootIndex, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": ociIndexMedia, "manifests": []any{map[string]any{"mediaType": rootMedia, "digest": indexDigest, "size": rootSize}}})
	if err := os.WriteFile(filepath.Join(layout, "index.json"), rootIndex, 0o600); err != nil {
		t.Fatal(err)
	}
	chartHash := digestHex(chart)
	installerHash := digestHex(installer)
	manifest := map[string]any{
		"schemaVersion": 1, "version": "1.2.3", "sourceRevision": testRevision,
		"chart":     map[string]string{"file": "kubeatlas-" + testChartVersion + ".tgz", "version": testChartVersion, "sha256": chartHash},
		"installer": map[string]string{"file": "install-hub-agent.sh", "sha256": installerHash},
		"image":     map[string]string{"digest": indexDigest},
	}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, indexDigest, layerDigest
}

func chartArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	content := []byte("apiVersion: v2\nname: kubeatlas\nversion: " + testChartVersion + "\nappVersion: 1.2.3\n")
	if err := writer.WriteHeader(&tar.Header{Name: "kubeatlas/Chart.yaml", Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func rewriteSelectedIndex(t *testing.T, dir string, change func(*indexDocument)) {
	t.Helper()
	manifestBytes, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	indexBytes := readBlobForTest(t, filepath.Join(dir, "oci"), manifest.Image.Digest)
	var index indexDocument
	if err := json.Unmarshal(indexBytes, &index); err != nil {
		t.Fatal(err)
	}
	change(&index)
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	digest := writeBlobForTest(t, filepath.Join(dir, "oci"), data)
	setImageRoot(t, dir, digest, ociIndexMedia, int64(len(data)))
}

func setImageRoot(t *testing.T, dir, digest, mediaType string, size int64) {
	t.Helper()
	manifestBytes, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Image.Digest = digest
	updatedManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), updatedManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := json.Marshal(indexDocument{SchemaVersion: 2, MediaType: ociIndexMedia, Manifests: []descriptor{{MediaType: mediaType, Digest: digest, Size: size}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "oci", "index.json"), root, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readBlobForTest(t *testing.T, layout, digest string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:")))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeBlobForTest(t *testing.T, layout string, data []byte) string {
	t.Helper()
	digest := digestBytes(data)
	if err := os.WriteFile(filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:")), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return digest
}

func digestBytes(data []byte) string { return "sha256:" + digestHex(data) }
func digestHex(data []byte) string   { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
