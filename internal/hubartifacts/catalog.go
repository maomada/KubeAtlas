package hubartifacts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	maxMetadataBytes = 8 << 20
	maxChartBytes    = 512 << 20
	maxInstallerSize = 8 << 20
	maxBlobSize      = int64(32 << 30)
	maxTotalBytes    = int64(64 << 30)
	maxChartEntries  = 10000
)

var (
	versionPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$`)
	chartVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	revisionPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type Manifest struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Version        string `json:"version"`
	SourceRevision string `json:"sourceRevision"`
	Chart          struct {
		File    string `json:"file"`
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	} `json:"chart"`
	Installer struct {
		File   string `json:"file"`
		SHA256 string `json:"sha256"`
	} `json:"installer"`
	Image struct {
		Digest string `json:"digest"`
	} `json:"image"`
}

type fileEntry struct {
	path      string
	mediaType string
	size      int64
	digest    string
	identity  fs.FileInfo
}

type rootSnapshot struct {
	path string
	info fs.FileInfo
}

type Catalog struct {
	root                string
	rootFS              *os.Root
	rootInfo            fs.FileInfo
	rootAncestors       []rootSnapshot
	version             string
	manifest            Manifest
	manifestPath        string
	chartPath           string
	installPath         string
	totalBytes          int64
	descriptorCount     int
	descriptorPlatforms map[string]string
	platforms           map[string]bool
	manifests           map[string]fileEntry
	blobs               map[string]fileEntry
	files               map[string]fileEntry
}

type Availability struct {
	Available       bool     `json:"available"`
	Version         string   `json:"version,omitempty"`
	ChartVersion    string   `json:"chartVersion,omitempty"`
	Platforms       []string `json:"platforms,omitempty"`
	InstallerSHA256 string   `json:"installerSHA256,omitempty"`
	ChartSHA256     string   `json:"chartSHA256,omitempty"`
	ChartFile       string   `json:"chartFile,omitempty"`
	ImageRepository string   `json:"imageRepository,omitempty"`
	ImageDigest     string   `json:"imageDigest,omitempty"`
}

func Open(dir, expectedVersion string) (*Catalog, error) {
	if dir == "" {
		return nil, nil
	}
	if !versionPattern.MatchString(expectedVersion) {
		return nil, errors.New("Portal build version is invalid")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Hub artifact directory is unavailable")
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, errors.New("Hub artifact directory is unavailable")
	}
	rootAncestors, err := snapshotDirectories(root)
	if err != nil {
		return nil, errors.New("Hub artifact directory path is unsafe")
	}
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return nil, errors.New("Hub artifact directory is unavailable")
	}
	keepRoot := false
	defer func() {
		if !keepRoot {
			_ = rootFS.Close()
		}
	}()
	rootInfo, err := rootFS.Stat(".")
	if err != nil || !os.SameFile(info, rootInfo) {
		return nil, errors.New("Hub artifact directory changed during startup")
	}
	manifestBytes, manifestEntry, err := readRootFile(rootFS, "manifest.json", maxMetadataBytes, "application/json")
	if err != nil || !uniqueJSON(manifestBytes) {
		return nil, errors.New("Hub artifact manifest is invalid")
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF ||
		manifest.SchemaVersion != 1 || manifest.Version != expectedVersion || !versionPattern.MatchString(manifest.Version) ||
		!revisionPattern.MatchString(manifest.SourceRevision) || !chartVersionPattern.MatchString(manifest.Chart.Version) ||
		manifest.Chart.File != "kubeatlas-"+manifest.Chart.Version+".tgz" || !validHex(manifest.Chart.SHA256) ||
		manifest.Installer.File != "install-hub-agent.sh" || !validHex(manifest.Installer.SHA256) ||
		!digestPattern.MatchString(manifest.Image.Digest) {
		return nil, errors.New("Hub artifact manifest does not match this Portal build")
	}
	chartEntry, err := verifyRootFile(rootFS, manifest.Chart.File, manifest.Chart.SHA256, maxChartBytes, "application/gzip")
	if err != nil {
		return nil, errors.New("Hub Agent chart is missing or has an invalid digest")
	}
	chart, _, err := openRootRegular(rootFS, manifest.Chart.File)
	if err != nil {
		return nil, errors.New("Hub Agent chart is unavailable")
	}
	chartVersion, appVersion, err := chartVersionsReader(chart)
	_ = chart.Close()
	if err != nil || chartVersion != manifest.Chart.Version || appVersion != manifest.Version {
		return nil, errors.New("Hub Agent chart version does not match the catalog")
	}
	installEntry, err := verifyRootFile(rootFS, manifest.Installer.File, manifest.Installer.SHA256, maxInstallerSize, "text/x-shellscript; charset=utf-8")
	if err != nil {
		return nil, errors.New("Hub Agent installer is missing or has an invalid digest")
	}
	catalog := &Catalog{root: root, rootFS: rootFS, rootInfo: rootInfo, rootAncestors: rootAncestors, version: manifest.Version, manifest: manifest,
		manifestPath: manifestEntry.path, chartPath: chartEntry.path, installPath: installEntry.path,
		platforms: map[string]bool{}, manifests: map[string]fileEntry{}, blobs: map[string]fileEntry{},
		files: map[string]fileEntry{manifestEntry.path: manifestEntry, chartEntry.path: chartEntry, installEntry.path: installEntry}}
	if err := catalog.validateLayout(); err != nil {
		return nil, err
	}
	keepRoot = true
	return catalog, nil
}

func snapshotDirectories(root string) ([]rootSnapshot, error) {
	volume := filepath.VolumeName(root)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(root, current)
	var snapshots []rootSnapshot
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("artifact root path contains a symlink or non-directory")
		}
		snapshots = append(snapshots, rootSnapshot{path: current, info: info})
	}
	return snapshots, nil
}

func (c *Catalog) Close() error {
	if c == nil || c.rootFS == nil {
		return nil
	}
	return c.rootFS.Close()
}

func (c *Catalog) Availability() Availability {
	if c == nil {
		return Availability{}
	}
	platforms := make([]string, 0, len(c.platforms))
	for platform, available := range c.platforms {
		if available {
			platforms = append(platforms, platform)
		}
	}
	sort.Strings(platforms)
	return Availability{Available: true, Version: c.version, ChartVersion: c.manifest.Chart.Version, Platforms: platforms, InstallerSHA256: c.manifest.Installer.SHA256,
		ChartSHA256: c.manifest.Chart.SHA256, ChartFile: c.manifest.Chart.File,
		ImageRepository: "kubeatlas/agent", ImageDigest: c.manifest.Image.Digest}
}

func openRootRegular(root *os.Root, relative string) (*os.File, fs.FileInfo, error) {
	if root == nil || !fs.ValidPath(relative) || strings.Contains(relative, "\\") {
		return nil, nil, errors.New("invalid artifact path")
	}
	parts := strings.Split(relative, "/")
	for i := range parts {
		name := strings.Join(parts[:i+1], "/")
		info, err := root.Lstat(name)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || i < len(parts)-1 && !info.IsDir() {
			return nil, nil, errors.New("artifact path is unavailable")
		}
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, errors.New("artifact is not a regular file")
	}
	var pathInfo fs.FileInfo
	for i := range parts {
		name := strings.Join(parts[:i+1], "/")
		pathInfo, err = root.Lstat(name)
		if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || i < len(parts)-1 && !pathInfo.IsDir() {
			_ = file.Close()
			return nil, nil, errors.New("artifact path changed during open")
		}
	}
	if !os.SameFile(info, pathInfo) {
		_ = file.Close()
		return nil, nil, errors.New("artifact file changed during open")
	}
	return file, info, nil
}

func readRootFile(root *os.Root, relative string, limit int64, mediaType string) ([]byte, fileEntry, error) {
	file, info, err := openRootRegular(root, relative)
	if err != nil {
		return nil, fileEntry{}, err
	}
	defer file.Close()
	if info.Size() < 0 || info.Size() > limit {
		return nil, fileEntry{}, errors.New("artifact metadata exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) != info.Size() {
		return nil, fileEntry{}, errors.New("artifact metadata is incomplete")
	}
	after, err := file.Stat()
	if err != nil || !sameSnapshot(info, after) {
		return nil, fileEntry{}, errors.New("artifact changed while opening")
	}
	return data, fileEntry{path: relative, mediaType: mediaType, size: info.Size(), digest: sha256Bytes(data), identity: info}, nil
}

func verifyRootFile(root *os.Root, relative, expected string, limit int64, mediaType string) (fileEntry, error) {
	file, info, err := openRootRegular(root, relative)
	if err != nil {
		return fileEntry{}, err
	}
	defer file.Close()
	if info.Size() < 0 || info.Size() > limit {
		return fileEntry{}, errors.New("artifact exceeds size limit")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil || hex.EncodeToString(hash.Sum(nil)) != expected {
		return fileEntry{}, errors.New("artifact digest mismatch")
	}
	after, err := file.Stat()
	if err != nil || !sameSnapshot(info, after) {
		return fileEntry{}, errors.New("artifact changed while opening")
	}
	return fileEntry{path: relative, mediaType: mediaType, size: info.Size(), digest: "sha256:" + expected, identity: info}, nil
}

func sameSnapshot(before, after fs.FileInfo) bool {
	return os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) && before.Mode().IsRegular() && after.Mode().IsRegular()
}

func chartVersionsReader(reader io.Reader) (string, string, error) {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return "", "", err
	}
	defer gz.Close()
	archive := tar.NewReader(gz)
	var metadata []byte
	count := 0
	var total int64
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", err
		}
		count++
		clean := filepath.ToSlash(filepath.Clean(header.Name))
		traversal := filepath.IsAbs(header.Name)
		for _, part := range strings.Split(filepath.ToSlash(header.Name), "/") {
			traversal = traversal || part == ".."
		}
		if count > maxChartEntries || traversal || clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") ||
			(header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA && header.Typeflag != tar.TypeDir) || header.Size < 0 {
			return "", "", errors.New("unsafe chart archive entry")
		}
		total += header.Size
		if total > maxChartBytes*4 {
			return "", "", errors.New("chart archive expands beyond size limit")
		}
		if filepath.Base(clean) == "Chart.yaml" {
			if metadata != nil || header.Size > 1<<20 {
				return "", "", errors.New("chart metadata is invalid")
			}
			metadata, err = io.ReadAll(io.LimitReader(archive, 1<<20+1))
			if err != nil || int64(len(metadata)) != header.Size {
				return "", "", errors.New("chart metadata is invalid")
			}
		} else if _, err := io.Copy(io.Discard, archive); err != nil {
			return "", "", err
		}
	}
	if metadata == nil {
		return "", "", errors.New("chart metadata is missing")
	}
	var chart struct {
		Name       string `yaml:"name"`
		Version    string `yaml:"version"`
		AppVersion string `yaml:"appVersion"`
	}
	if err := yaml.Unmarshal(metadata, &chart); err != nil || chart.Name != "kubeatlas" {
		return "", "", errors.New("chart metadata is invalid")
	}
	return chart.Version, chart.AppVersion, nil
}

func validHex(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func uniqueJSON(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 32 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] || !walk(depth+1) {
					return false
				}
				seen[name] = true
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for decoder.More() {
				if !walk(depth + 1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	return walk(0) && decoder.Decode(new(any)) == io.EOF
}

func sha256Bytes(data []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}
