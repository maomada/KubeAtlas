package updater

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestZip(t *testing.T, names, contents []string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "update.zip")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for i, name := range names {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(contents[i])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestExtractZipSizeLimit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		contents  []string
		limit     int64
		wantError bool
	}{
		{"exact aggregate boundary", []string{"1234", "5678"}, 8, false},
		{"single oversized entry", []string{strings.Repeat("x", 1024)}, 8, true},
		{"aggregate oversized entries", []string{"1234", "5678"}, 7, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			names := []string{"bin/app", "bin/data"}[:len(tc.contents)]
			archive := writeTestZip(t, names, tc.contents)
			dest := t.TempDir()
			err := extractZipWithLimit(archive, dest, tc.limit)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "extracted size limit") {
					t.Fatalf("error = %v, want size limit rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for i, name := range names {
				got, err := os.ReadFile(filepath.Join(dest, name))
				if err != nil || string(got) != tc.contents[i] {
					t.Fatalf("%s = %q, %v", name, got, err)
				}
			}
		})
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	archive := writeTestZip(t, []string{"../outside"}, []string{"data"})
	if err := extractZip(archive, t.TempDir()); err == nil || !strings.Contains(err.Error(), "illegal file path") {
		t.Fatalf("error = %v, want traversal rejection", err)
	}
}
