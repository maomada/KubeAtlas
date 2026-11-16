package hubartifacts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"math"
	"testing"
)

func TestChartVersionsReaderSkipsPayloadBeforeMetadata(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	writeChartTestEntry(t, writer, "kubeatlas/templates/workload.yaml", bytes.Repeat([]byte{'x'}, 513))
	writeChartTestEntry(t, writer, "kubeatlas/Chart.yaml", chartTestMetadata())
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	version, appVersion, err := chartVersionsReader(bytes.NewReader(gzipChartTestArchive(t, archive.Bytes())))
	if err != nil || version != testChartVersion || appVersion != "1.2.3" {
		t.Fatalf("metadata after payload: version=%q appVersion=%q err=%v", version, appVersion, err)
	}
}

func TestChartVersionsReaderRejectsTruncatedPayloadAfterMetadata(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	writeChartTestEntry(t, writer, "kubeatlas/Chart.yaml", chartTestMetadata())
	if err := writer.WriteHeader(&tar.Header{Name: "kubeatlas/templates/workload.yaml", Mode: 0o600, Size: 1024, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	// Deliberately omit tar.Close: the gzip is valid, but the declared entry is short.
	version, appVersion, err := chartVersionsReader(bytes.NewReader(gzipChartTestArchive(t, archive.Bytes())))
	if !errors.Is(err, io.ErrUnexpectedEOF) || version != "" || appVersion != "" {
		t.Fatalf("truncated payload accepted: version=%q appVersion=%q err=%v", version, appVersion, err)
	}
}

func TestChartVersionsReaderRejectsExpandedSizeOverflowBeforePayload(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	writeChartTestEntry(t, writer, "kubeatlas/Chart.yaml", chartTestMetadata())
	if err := writer.WriteHeader(&tar.Header{
		Name: "kubeatlas/templates/workload.yaml", Mode: 0o600,
		Size: math.MaxInt64, Typeflag: tar.TypeReg, Format: tar.FormatGNU,
	}); err != nil {
		t.Fatal(err)
	}
	// The tiny archive contains no payload for this header. Reject its declared
	// size before attempting a read, even after metadata has increased total.
	version, appVersion, err := chartVersionsReader(bytes.NewReader(gzipChartTestArchive(t, archive.Bytes())))
	if err == nil || err.Error() != "chart archive expands beyond size limit" || version != "" || appVersion != "" {
		t.Fatalf("oversized entry reached its payload: version=%q appVersion=%q err=%v", version, appVersion, err)
	}
}

func chartTestMetadata() []byte {
	return []byte("apiVersion: v2\nname: kubeatlas\nversion: " + testChartVersion + "\nappVersion: 1.2.3\n")
}

func writeChartTestEntry(t *testing.T, writer *tar.Writer, name string, data []byte) {
	t.Helper()
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
}

func gzipChartTestArchive(t *testing.T, data []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}
