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
	"testing"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"

func TestOpenAndReadOnlyArtifactRoutes(t *testing.T) {
	dir, digest, layerDigest := makeBundle(t)
	catalog, err := Open(dir, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog.Availability(); !got.Available || got.ImageDigest != digest || got.ChartSHA256 == "" {
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

func TestOpenAllowsMissingCatalogOnlyWhenUnconfigured(t *testing.T) {
	if catalog, err := Open("", ""); err != nil || catalog != nil {
		t.Fatalf("unconfigured catalog = %v, %v", catalog, err)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "missing"), "1.2.3"); err == nil {
		t.Fatal("configured missing catalog must fail")
	}
}

func makeBundle(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	chart := chartArchive(t)
	installer := []byte("#!/usr/bin/env bash\nexit 0\n")
	if err := os.WriteFile(filepath.Join(dir, "kubeatlas-1.2.3.tgz"), chart, 0o600); err != nil {
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
	platforms := []string{"amd64", "arm64"}
	manifests := make([]map[string]any, 0, len(platforms))
	for _, architecture := range platforms {
		config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": architecture, "config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.version": "1.2.3", "org.opencontainers.image.revision": testRevision}}})
		configDigest := put(config)
		manifest, _ := json.Marshal(map[string]any{
			"schemaVersion": 2, "mediaType": ociImageMedia,
			"config": map[string]any{"mediaType": ociConfigMedia, "digest": configDigest, "size": len(config)},
			"layers": []any{map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": layerDigest, "size": len(layer)}},
		})
		manifestDigest := put(manifest)
		manifests = append(manifests, map[string]any{
			"mediaType": ociImageMedia, "digest": manifestDigest, "size": len(manifest),
			"platform": map[string]string{"os": "linux", "architecture": architecture},
		})
	}
	index, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": ociIndexMedia, "manifests": manifests})
	indexDigest := put(index)
	if err := os.WriteFile(filepath.Join(layout, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rootIndex, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": ociIndexMedia, "manifests": []any{map[string]any{"mediaType": ociIndexMedia, "digest": indexDigest, "size": len(index)}}})
	if err := os.WriteFile(filepath.Join(layout, "index.json"), rootIndex, 0o600); err != nil {
		t.Fatal(err)
	}
	chartHash := digestHex(chart)
	installerHash := digestHex(installer)
	manifest := map[string]any{
		"schemaVersion": 1, "version": "1.2.3", "sourceRevision": testRevision,
		"chart":     map[string]string{"file": "kubeatlas-1.2.3.tgz", "version": "1.2.3", "sha256": chartHash},
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
	content := []byte("apiVersion: v2\nname: kubeatlas\nversion: 1.2.3\nappVersion: 1.2.3\n")
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

func digestBytes(data []byte) string { return "sha256:" + digestHex(data) }
func digestHex(data []byte) string   { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
