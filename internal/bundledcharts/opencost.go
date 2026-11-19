package bundledcharts

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const OpenCostName = "opencost"

const (
	maxOpenCostArchive  = 8 << 20
	maxOpenCostExpanded = 32 << 20
)

var (
	ErrOpenCostUnavailable = errors.New("operator-managed OpenCost package is unavailable")
	digestPattern          = regexp.MustCompile("^[0-9a-f]{64}$")
	registryPattern        = regexp.MustCompile("^([a-z0-9]+(?:[.-][a-z0-9]+)*)(?::([0-9]{1,5}))?$")
	imagePathPattern       = regexp.MustCompile("^[a-z0-9]+(?:[._-]+[a-z0-9]+)*(?:/[a-z0-9]+(?:[._-]+[a-z0-9]+)*)*@sha256:[0-9a-f]{64}$")
)

func loadOpenCost(version string) (*chart.Chart, Source, error) {
	archivePath := os.Getenv("KUBEATLAS_OPENCOST_CHART_PATH")
	expected := os.Getenv("KUBEATLAS_OPENCOST_CHART_SHA256")
	registry := os.Getenv("KUBEATLAS_OPENCOST_IMAGE_REGISTRY")
	if !filepath.IsAbs(archivePath) || filepath.Ext(archivePath) != ".tgz" ||
		!digestPattern.MatchString(expected) || !validImageRegistry(registry) {
		return nil, Source{}, ErrOpenCostUnavailable
	}
	data, err := readOpenCostArchive(archivePath)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != expected {
		return nil, Source{}, fmt.Errorf("%w: archive could not be read or verified", ErrOpenCostUnavailable)
	}
	loaded, err := verifiedOpenCostChart(data)
	if err != nil || loaded.Metadata.Name != OpenCostName || !embeddedDependencies(loaded) || len(loaded.CRDObjects()) != 0 {
		return nil, Source{}, fmt.Errorf("%w: chart is invalid or dependencies are not prepared", ErrOpenCostUnavailable)
	}
	if version != "" && version != loaded.Metadata.Version {
		return nil, Source{}, fmt.Errorf("%w: selected version is no longer available", ErrOpenCostUnavailable)
	}
	renderValues, err := chartutil.ToRenderValues(loaded, nil, chartutil.ReleaseOptions{
		Name: OpenCostName, Namespace: OpenCostName, Revision: 1, IsInstall: true,
	}, nil)
	if err != nil {
		return nil, Source{}, fmt.Errorf("%w: chart values could not be rendered", ErrOpenCostUnavailable)
	}
	manifests, err := engine.Render(loaded, renderValues)
	if err != nil {
		return nil, Source{}, fmt.Errorf("%w: chart could not be rendered", ErrOpenCostUnavailable)
	}
	var combined bytes.Buffer
	for name, manifest := range manifests {
		if filepath.Base(name) != "NOTES.txt" && strings.TrimSpace(manifest) != "" {
			combined.WriteString("\n---\n")
			combined.WriteString(manifest)
		}
	}
	if err := ValidateOpenCostManifests(combined.Bytes(), registry); err != nil {
		return nil, Source{}, fmt.Errorf("%w: %v", ErrOpenCostUnavailable, err)
	}
	return loaded, Source{Name: OpenCostName, Version: loaded.Metadata.Version, Repository: Repository, Digest: expected, ImageRegistry: registry}, nil
}

func readOpenCostArchive(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxOpenCostArchive {
		return nil, ErrOpenCostUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil || !os.SameFile(before, current) {
		return nil, ErrOpenCostUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, maxOpenCostArchive+1))
	if err != nil || len(data) > maxOpenCostArchive {
		return nil, ErrOpenCostUnavailable
	}
	return data, nil
}

func verifiedOpenCostChart(data []byte) (*chart.Chart, error) {
	uncompressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	size, err := io.Copy(io.Discard, io.LimitReader(uncompressed, maxOpenCostExpanded+1))
	uncompressed.Close()
	if err != nil || size > maxOpenCostExpanded {
		return nil, ErrOpenCostUnavailable
	}
	files, err := loader.LoadArchiveFiles(bytes.NewReader(data))
	if err != nil || len(files) > 1024 {
		return nil, ErrOpenCostUnavailable
	}
	names := make(map[string]bool, len(files))
	for _, file := range files {
		if names[file.Name] || strings.HasSuffix(strings.ToLower(file.Name), ".tgz") {
			return nil, ErrOpenCostUnavailable
		}
		names[file.Name] = true
	}
	return loader.LoadFiles(files)
}

func embeddedDependencies(loaded *chart.Chart) bool {
	for _, requested := range loaded.Metadata.Dependencies {
		found := false
		for _, bundled := range loaded.Dependencies() {
			if bundled.Name() == requested.Name && chartutil.IsCompatibleRange(requested.Version, bundled.Metadata.Version) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, dependency := range loaded.Dependencies() {
		if !embeddedDependencies(dependency) {
			return false
		}
	}
	return true
}

func validImageRegistry(registry string) bool {
	if address, ok := strings.CutPrefix(registry, "["); ok {
		host, suffix, found := strings.Cut(address, "]")
		ip, err := netip.ParseAddr(host)
		if !found || err != nil || !ip.Is6() || ip.Zone() != "" {
			return false
		}
		if suffix == "" {
			return true
		}
		port, ok := strings.CutPrefix(suffix, ":")
		return ok && validImageRegistryPort(port)
	}
	matches := registryPattern.FindStringSubmatch(registry)
	if len(matches) == 0 || len(matches[1]) > 253 || !strings.ContainsAny(registry, ".:") {
		return false
	}
	if matches[2] != "" {
		return validImageRegistryPort(matches[2])
	}
	return true
}

func validImageRegistryPort(value string) bool {
	if len(value) == 0 || len(value) > 5 || strings.ContainsAny(value, "+-") {
		return false
	}
	port, err := strconv.Atoi(value)
	return err == nil && port > 0 && port <= 65535
}

func ValidateOpenCostManifests(data []byte, registry string) error {
	if len(data) > maxOpenCostExpanded || !validImageRegistry(registry) {
		return errors.New("manifest or approved image registry is invalid")
	}
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	var images int
	for {
		var document map[string]any
		if err := decoder.Decode(&document); err == io.EOF {
			break
		} else if err != nil {
			return errors.New("rendered manifest is invalid")
		}
		if !validatedManifest(document, registry+"/", &images) {
			return errors.New("chart contains a hook, CRD, or image without a verified registry and SHA-256 digest")
		}
	}
	if images == 0 {
		return errors.New("chart does not declare an OpenCost workload image")
	}
	return nil
}

func validatedManifest(value any, imagePrefix string, images *int) bool {
	switch typed := value.(type) {
	case map[string]any:
		if kind, _ := typed["kind"].(string); kind == "CustomResourceDefinition" {
			return false
		}
		if _, hook := typed["helm.sh/hook"]; hook {
			return false
		}
		for key, field := range typed {
			if key == "image" {
				reference, ok := field.(string)
				if source, volume := field.(map[string]any); volume {
					reference, ok = source["reference"].(string)
				}
				if !ok || !strings.HasPrefix(reference, imagePrefix) ||
					!imagePathPattern.MatchString(strings.TrimPrefix(reference, imagePrefix)) {
					return false
				}
				*images++
			}
			if !validatedManifest(field, imagePrefix, images) {
				return false
			}
		}
	case []any:
		for _, field := range typed {
			if !validatedManifest(field, imagePrefix, images) {
				return false
			}
		}
	}
	return true
}
