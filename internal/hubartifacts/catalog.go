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
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	maxMetadataBytes = 8 << 20
	maxChartBytes    = 512 << 20
	maxInstallerSize = 8 << 20
	maxBlobSize      = int64(32 << 30)
	maxTotalBytes    = int64(64 << 30)
)

var (
	versionPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$`)
	revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
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
}

type Catalog struct {
	root         string
	version      string
	manifest     Manifest
	manifestPath string
	chartPath    string
	installPath  string
	layoutRoot   string
	totalBytes   int64
	platforms    map[string]bool
	manifests    map[string]fileEntry
	blobs        map[string]fileEntry
}

type Availability struct {
	Available       bool   `json:"available"`
	Version         string `json:"version,omitempty"`
	InstallerSHA256 string `json:"installerSHA256,omitempty"`
	ChartSHA256     string `json:"chartSHA256,omitempty"`
	ChartFile       string `json:"chartFile,omitempty"`
	ImageRepository string `json:"imageRepository,omitempty"`
	ImageDigest     string `json:"imageDigest,omitempty"`
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
	manifestPath, err := regularPath(root, "manifest.json")
	if err != nil {
		return nil, err
	}
	manifestBytes, err := readBounded(manifestPath, maxMetadataBytes)
	if err != nil || !uniqueJSON(manifestBytes) {
		return nil, errors.New("Hub artifact manifest is invalid")
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF ||
		manifest.SchemaVersion != 1 || manifest.Version != expectedVersion || !versionPattern.MatchString(manifest.Version) ||
		!revisionPattern.MatchString(manifest.SourceRevision) || manifest.Chart.File != "kubeatlas-"+manifest.Version+".tgz" ||
		manifest.Chart.Version != manifest.Version || !validHex(manifest.Chart.SHA256) ||
		manifest.Installer.File != "install-hub-agent.sh" || !validHex(manifest.Installer.SHA256) ||
		!digestPattern.MatchString(manifest.Image.Digest) {
		return nil, errors.New("Hub artifact manifest does not match this Portal build")
	}
	chartPath, err := regularPath(root, manifest.Chart.File)
	if err != nil || verifyFile(chartPath, manifest.Chart.SHA256, maxChartBytes) != nil {
		return nil, errors.New("Hub Agent chart is missing or has an invalid digest")
	}
	chartVersion, appVersion, err := chartVersions(chartPath)
	if err != nil || chartVersion != manifest.Version || appVersion != manifest.Version {
		return nil, errors.New("Hub Agent chart version does not match the catalog")
	}
	installPath, err := regularPath(root, manifest.Installer.File)
	if err != nil || verifyFile(installPath, manifest.Installer.SHA256, maxInstallerSize) != nil {
		return nil, errors.New("Hub Agent installer is missing or has an invalid digest")
	}
	catalog := &Catalog{root: root, version: manifest.Version, manifest: manifest, manifestPath: manifestPath,
		chartPath: chartPath, installPath: installPath, platforms: map[string]bool{}, manifests: map[string]fileEntry{}, blobs: map[string]fileEntry{}}
	if err := catalog.validateLayout(); err != nil {
		return nil, err
	}
	return catalog, nil
}

func (c *Catalog) Availability() Availability {
	if c == nil {
		return Availability{}
	}
	return Availability{Available: true, Version: c.version, InstallerSHA256: c.manifest.Installer.SHA256,
		ChartSHA256: c.manifest.Chart.SHA256, ChartFile: c.manifest.Chart.File,
		ImageRepository: "kubeatlas/agent", ImageDigest: c.manifest.Image.Digest}
}

func regularPath(root, relative string) (string, error) {
	if !fs.ValidPath(relative) || filepath.IsAbs(relative) || strings.Contains(relative, "\\") {
		return "", errors.New("invalid artifact path")
	}
	path := root
	for _, part := range strings.Split(relative, "/") {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("artifact path is unavailable")
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("artifact is not a regular file")
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("artifact path escapes bundle")
	}
	return path, nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() < 0 || info.Size() > limit {
		return nil, errors.New("artifact metadata exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("artifact metadata exceeds size limit")
	}
	return data, nil
}

func verifyFile(path, expected string, limit int64) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() < 0 || info.Size() > limit {
		return errors.New("artifact exceeds size limit")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil || hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("artifact digest mismatch")
	}
	return nil
}

func chartVersions(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
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
		if count > 10000 || traversal || clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") ||
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
