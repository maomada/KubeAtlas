package hubartifacts

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	bundleRoot         = "hub-agent-bundle"
	maxBundleFiles     = 2*maxTotalDescriptors + 32
	maxBundleBytes     = 2*maxTotalBytes + 2*int64(maxChartBytes)
	maxBundleNotice    = 1 << 20
	maxBundlePathBytes = 200
)

var bundleNotices = []string{"LICENSE", "NOTICE"}

// bundleFiles includes only files required by the verified catalog and its notices.
func (c *Catalog) bundleFiles() ([]fileEntry, error) {
	if !c.Availability().BootstrapAvailable {
		return nil, errors.New("Hub Agent bundle requires a bootstrap script")
	}
	files := make([]fileEntry, 0, len(c.files)+len(c.agentOCI.blobs)+6)
	for _, entry := range c.files {
		files = append(files, entry)
	}
	for _, repository := range []*ociRepository{c.agentOCI, c.openCostOCI} {
		if repository == nil {
			continue
		}
		for _, entry := range repository.blobs {
			files = append(files, entry)
		}
		for _, name := range []string{"index.json", "oci-layout"} {
			_, entry, err := readRootFile(c.rootFS, path.Join(repository.path, name), maxMetadataBytes, "application/json")
			if err != nil {
				return nil, err
			}
			files = append(files, entry)
		}
	}
	for _, name := range bundleNotices {
		_, entry, err := readRootFile(c.rootFS, name, maxBundleNotice, "text/plain")
		if err != nil || entry.size == 0 {
			return nil, fmt.Errorf("Hub Agent bundle requires a valid %s file", name)
		}
		files = append(files, entry)
	}
	slices.SortFunc(files, func(a, b fileEntry) int { return strings.Compare(a.path, b.path) })
	return files, nil
}

func (c *Catalog) writeBundleArchive(destination string) error {
	files, err := c.bundleFiles()
	if err != nil {
		return err
	}
	output, err := newBundleDestination(destination)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".hub-agent-archive-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := c.writeBundleContents(file, files); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Link atomically without replacing a destination created by another writer.
	return os.Link(file.Name(), output)
}

func (c *Catalog) writeBundleContents(output io.Writer, files []fileEntry) error {
	compressed, err := gzip.NewWriterLevel(output, gzip.BestSpeed)
	if err != nil {
		return err
	}
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: bundleRoot + "/", Mode: 0o555, Typeflag: tar.TypeDir, Format: tar.FormatUSTAR}); err != nil {
		return err
	}
	for _, entry := range files {
		if err := c.writeBundleEntry(archive, entry); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return compressed.Close()
}

func (c *Catalog) writeBundleEntry(archive *tar.Writer, entry fileEntry) error {
	file, info, err := openRootRegular(c.rootFS, entry.path)
	if err != nil {
		return err
	}
	defer file.Close()
	if !sameSnapshot(entry.identity, info) {
		return fmt.Errorf("artifact %s changed while creating the bundle", entry.path)
	}
	header := &tar.Header{Name: bundleRoot + "/" + entry.path, Size: entry.size, Mode: 0o444, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}
	if err := archive.WriteHeader(header); err != nil {
		return err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(file, entry.size+1))
	if err != nil || written != entry.size || fmt.Sprintf("sha256:%x", hash.Sum(nil)) != entry.digest {
		return fmt.Errorf("could not copy the verified bytes of artifact %s", entry.path)
	}
	return nil
}

// ImportArchive verifies and extracts an Agent catalog into a fresh directory.
// It publishes the directory only after the same validation used at Hub startup.
func ImportArchive(archivePath, checksum, destination, expectedVersion string) error {
	if !validHex(checksum) || !versionPattern.MatchString(expectedVersion) {
		return errors.New("Hub Agent bundle requires a SHA-256 checksum and exact build version")
	}
	output, err := newBundleDestination(destination)
	if err != nil {
		return err
	}
	info, err := os.Lstat(archivePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBundleBytes {
		return errors.New("Hub Agent bundle is unavailable or exceeds the archive size limit")
	}
	input, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !sameSnapshot(info, opened) {
		return errors.New("Hub Agent bundle changed before verification")
	}
	if err := verifyDigestReader(input, "sha256:"+checksum, info.Size()); err != nil {
		return errors.New("Hub Agent bundle SHA-256 checksum does not match")
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(output), ".hub-agent-import-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	hash := sha256.New()
	files, err := unpackBundle(io.TeeReader(io.LimitReader(input, info.Size()+1), hash), stage)
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != checksum {
		return errors.New("Hub Agent bundle changed during extraction")
	}
	catalog, err := Open(stage, expectedVersion)
	if err != nil {
		return err
	}
	expected, err := catalog.bundleFiles()
	_ = catalog.Close()
	if err != nil {
		return err
	}
	if len(expected) != len(files) || slices.ContainsFunc(expected, func(entry fileEntry) bool { return !files[entry.path] }) {
		return errors.New("Hub Agent bundle contains files outside its catalog")
	}
	if err := makeReadOnly(stage); err != nil {
		return err
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("Hub Agent bundle destination appeared during import")
	}
	return os.Rename(stage, output)
}

func newBundleDestination(destination string) (string, error) {
	if destination == "" {
		return "", errors.New("Hub Agent bundle destination is required")
	}
	output, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("Hub Agent bundle destination already exists or cannot be inspected")
	}
	parents, err := snapshotDirectories(filepath.Dir(output))
	if err != nil || len(parents) == 0 || parents[len(parents)-1].info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("Hub Agent bundle destination parent must be an existing directory protected from other writers")
	}
	return output, nil
}

func unpackBundle(input io.Reader, directory string) (map[string]bool, error) {
	buffer := bufio.NewReader(input)
	compressed, err := gzip.NewReader(buffer)
	if err != nil {
		return nil, err
	}
	defer compressed.Close()
	compressed.Multistream(false)
	expanded := &io.LimitedReader{R: compressed, N: maxBundleBytes + 1}
	archive := tar.NewReader(expanded)
	header, err := archive.Next()
	if err != nil || header.Typeflag != tar.TypeDir || header.Name != bundleRoot+"/" {
		return nil, errors.New("Hub Agent bundle has an invalid archive root")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := make(map[string]bool)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name, ok := strings.CutPrefix(header.Name, bundleRoot+"/")
		limit := bundleFileLimit(name)
		if !ok || header.Typeflag != tar.TypeReg || limit == 0 || header.Size < 0 || header.Size > limit ||
			files[name] || len(files) == maxBundleFiles {
			return nil, fmt.Errorf("Hub Agent bundle contains an invalid archive member %q", header.Name)
		}
		if err := extractBundleFile(archive, root, name, header.Size); err != nil {
			return nil, err
		}
		files[name] = true
	}
	// Check the gzip footer without expanding any extra tar payload.
	var trailing [1]byte
	if _, err := io.ReadFull(expanded, trailing[:]); !errors.Is(err, io.EOF) || expanded.N == 0 {
		return nil, errors.New("Hub Agent bundle has an invalid or oversized archive payload")
	}
	if _, err := buffer.Peek(1); !errors.Is(err, io.EOF) {
		return nil, errors.New("Hub Agent bundle has trailing data")
	}
	return files, nil
}

func bundleFileLimit(name string) int64 {
	if len(name) > maxBundlePathBytes || strings.ContainsAny(name, "\\:") || !filepath.IsLocal(name) || path.Clean(name) != name {
		return 0
	}
	switch name {
	case "manifest.json":
		return maxMetadataBytes
	case "install-hub-agent.sh", "bootstrap-hub-agent.sh":
		return maxInstallerSize
	case openCostChartName:
		return maxOpenCostChart
	case "LICENSE", "NOTICE":
		return maxBundleNotice
	}
	if chart, ok := strings.CutPrefix(name, "kubeatlas-"); ok {
		if version, ok := strings.CutSuffix(chart, ".tgz"); ok && chartVersionPattern.MatchString(version) {
			return maxChartBytes
		}
	}
	for _, repository := range []string{agentOCIPath, openCostOCIPath} {
		if file, ok := strings.CutPrefix(name, repository+"/"); ok {
			switch file {
			case "index.json", "oci-layout":
				return maxMetadataBytes
			}
			if digest, ok := strings.CutPrefix(file, "blobs/sha256/"); ok && validHex(digest) {
				return maxBlobSize
			}
		}
	}
	return 0
}

func extractBundleFile(archive io.Reader, root *os.Root, name string, size int64) error {
	local := filepath.FromSlash(name)
	if err := root.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return err
	}
	file, err := root.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(file, archive, size)
	return errors.Join(copyErr, file.Close())
}
