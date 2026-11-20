package hubartifacts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHubAgentBundleRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bundle installation checks POSIX directory modes")
	}
	dir, imageDigest, _ := makeBundle(t)
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	bootstrap := []byte("#!/usr/bin/env bash\nexit 0\n")
	manifest.Bootstrap = &struct {
		File   string `json:"file"`
		SHA256 string `json:"sha256"`
	}{File: "bootstrap-hub-agent.sh", SHA256: digestHex(bootstrap)}
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string][]byte{
		"manifest.json": data, "bootstrap-hub-agent.sh": bootstrap, "LICENSE": []byte("license"),
		"NOTICE": []byte("notice"), "not-an-artifact.txt": []byte("must not be distributed"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := Open(dir, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	archive := privateOutputPath(t, "release.tar.gz")
	if err := catalog.writeBundleArchive(archive); err != nil {
		t.Fatal(err)
	}
	checksum, err := hashPath(archive)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(filepath.Dir(archive), "installed")
	cleanupReadOnlyBundle(t, dest)
	if err := ImportArchive(archive, strings.Repeat("0", 64), dest, "1.2.3"); err == nil {
		t.Fatal("unexpected success with a wrong checksum")
	}
	if err := ImportArchive(archive, checksum, dest, "1.2.4"); err == nil {
		t.Fatal("unexpected success with a mismatched build version")
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed import modified its destination: %v", err)
	}
	if err := ImportArchive(archive, checksum, dest, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	installed, err := Open(dest, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	defer installed.Close()
	if availability := installed.Availability(); !availability.BootstrapAvailable || availability.ImageDigest != imageDigest || len(availability.Platforms) != 2 {
		t.Fatalf("installed catalog differs from packaged artifact: %+v", availability)
	}
	if _, err := os.Stat(filepath.Join(dest, "not-an-artifact.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bundle included an unrelated file: %v", err)
	}
	if notice, err := os.ReadFile(filepath.Join(dest, "NOTICE")); err != nil || string(notice) != "notice" {
		t.Fatalf("notice has incorrect contents %q or read error %v", notice, err)
	}
	if err := ImportArchive(archive, checksum, dest, "1.2.3"); err == nil {
		t.Fatal("re-import succeeded over an existing catalog")
	}
}

func TestHubAgentBundleRejectsUnsafeArchiveMembers(t *testing.T) {
	tests := []struct {
		name           string
		headers        []*tar.Header
		suffix         []byte
		expandedSuffix []byte
		corruptFooter  bool
	}{
		{name: "path traversal", headers: []*tar.Header{{Name: bundleRoot + "/../escape", Typeflag: tar.TypeReg}}},
		{name: "symbolic link", headers: []*tar.Header{{Name: bundleRoot + "/manifest.json", Typeflag: tar.TypeSymlink, Linkname: "../escape"}}},
		{name: "hard link", headers: []*tar.Header{{Name: bundleRoot + "/LICENSE", Typeflag: tar.TypeLink, Linkname: "../escape"}}},
		{name: "unknown payload", headers: []*tar.Header{{Name: bundleRoot + "/server.env", Typeflag: tar.TypeReg}}},
		{name: "duplicate file", headers: []*tar.Header{
			{Name: bundleRoot + "/manifest.json", Typeflag: tar.TypeReg},
			{Name: bundleRoot + "/manifest.json", Typeflag: tar.TypeReg},
		}},
		{name: "trailing data", suffix: []byte("trailing bytes")},
		{name: "trailing expanded data", expandedSuffix: []byte("trailing bytes")},
		{name: "corrupt gzip footer", corruptFooter: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload bytes.Buffer
			compressed := gzip.NewWriter(&payload)
			archive := tar.NewWriter(compressed)
			if err := archive.WriteHeader(&tar.Header{Name: bundleRoot + "/", Typeflag: tar.TypeDir}); err != nil {
				t.Fatal(err)
			}
			for _, h := range tt.headers {
				if err := archive.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := compressed.Write(tt.expandedSuffix); err != nil {
				t.Fatal(err)
			}
			if err := compressed.Close(); err != nil {
				t.Fatal(err)
			}
			if tt.corruptFooter {
				payload.Bytes()[payload.Len()-8] ^= 0xff
			}
			payload.Write(tt.suffix)
			stage := t.TempDir()
			if _, err := unpackBundle(&payload, stage); err == nil {
				t.Fatal("unsafe archive passed extraction")
			}
		})
	}
}
