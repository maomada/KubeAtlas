package hubartifacts

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	ociIndexMedia          = "application/vnd.oci.image.index.v1+json"
	dockerIndexMedia       = "application/vnd.docker.distribution.manifest.list.v2+json"
	ociImageMedia          = "application/vnd.oci.image.manifest.v1+json"
	dockerImageMedia       = "application/vnd.docker.distribution.manifest.v2+json"
	ociConfigMedia         = "application/vnd.oci.image.config.v1+json"
	dockerConfigMedia      = "application/vnd.docker.container.image.v1+json"
	maxGraphDepth          = 16
	maxDescriptorsPerIndex = 128
	maxTotalDescriptors    = 4096
)

type descriptor struct {
	MediaType string   `json:"mediaType"`
	Digest    string   `json:"digest"`
	Size      int64    `json:"size"`
	URLs      []string `json:"urls"`
	Platform  *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
}

type indexDocument struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Manifests     []descriptor `json:"manifests"`
}

type imageDocument struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

type imageConfiguration struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Config       struct {
		Labels map[string]string `json:"Labels"`
	} `json:"config"`
}

func (c *Catalog) validateLayout() error {
	layoutPath := filepath.Join(c.root, "oci")
	info, err := os.Lstat(layoutPath)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Hub OCI layout is unavailable")
	}
	layoutBytes, _, err := readRootFile(c.rootFS, "oci/oci-layout", 64<<10, "application/json")
	if err != nil || !uniqueJSON(layoutBytes) {
		return errors.New("Hub OCI layout metadata is invalid")
	}
	var layout struct {
		Version string `json:"imageLayoutVersion"`
	}
	if json.Unmarshal(layoutBytes, &layout) != nil || layout.Version != "1.0.0" {
		return errors.New("Hub OCI layout version is unsupported")
	}
	indexBytes, _, err := readRootFile(c.rootFS, "oci/index.json", maxMetadataBytes, "application/json")
	if err != nil || !uniqueJSON(indexBytes) {
		return errors.New("Hub OCI index is invalid")
	}
	var root indexDocument
	if json.Unmarshal(indexBytes, &root) != nil || root.SchemaVersion != 2 || len(root.Manifests) == 0 || len(root.Manifests) > maxDescriptorsPerIndex ||
		root.MediaType != "" && root.MediaType != ociIndexMedia {
		return errors.New("Hub OCI index is unsupported")
	}
	var selected *descriptor
	for i := range root.Manifests {
		d := &root.Manifests[i]
		if d.Digest == c.manifest.Image.Digest {
			if selected != nil {
				return errors.New("Hub OCI layout has an ambiguous image digest")
			}
			selected = d
		}
	}
	if selected == nil {
		return errors.New("Hub OCI image digest is not present in the local layout")
	}
	if selected.MediaType != ociIndexMedia && selected.MediaType != dockerIndexMedia && selected.MediaType != ociImageMedia && selected.MediaType != dockerImageMedia {
		return errors.New("Hub OCI Agent image uses an unsupported root media type")
	}
	c.totalBytes = 0
	c.descriptorCount = 0
	c.descriptorPlatforms = map[string]string{}
	if err := c.walkDescriptor(*selected, 1, map[string]bool{}, map[string]bool{}); err != nil {
		return err
	}
	if len(c.platforms) == 0 {
		return errors.New("Hub OCI image has no supported Linux platform")
	}
	return nil
}

func (c *Catalog) walkDescriptor(d descriptor, depth int, visiting, visited map[string]bool) error {
	if depth > maxGraphDepth {
		return errors.New("Hub OCI descriptor graph exceeds depth limit")
	}
	if err := c.countDescriptor(); err != nil {
		return err
	}
	if !descriptorMediaType(d.MediaType) || !digestPattern.MatchString(d.Digest) || d.Size < 0 || d.Size > maxBlobSize || len(d.URLs) != 0 {
		return errors.New("Hub OCI descriptor is invalid or contains a foreign URL")
	}
	declaredPlatform := ""
	if d.Platform != nil {
		if !supportedPlatform(d.Platform.OS, d.Platform.Architecture) {
			return errors.New("Hub OCI descriptor platform is unsupported")
		}
		declaredPlatform = d.Platform.OS + "/" + d.Platform.Architecture
	}
	if visited[d.Digest] {
		entry, ok := c.blobs[d.Digest]
		if !ok || entry.mediaType != d.MediaType || entry.size != d.Size {
			return errors.New("Hub OCI descriptor has conflicting media type or size")
		}
		if d.MediaType == ociImageMedia || d.MediaType == dockerImageMedia {
			if platform, exists := c.descriptorPlatforms[d.Digest]; !exists || declaredPlatform != "" && platform != declaredPlatform {
				return errors.New("Hub OCI image descriptor has conflicting platform metadata")
			}
			return errors.New("Hub OCI image index contains a duplicate image descriptor")
		}
		return nil
	}
	if visiting[d.Digest] {
		return errors.New("Hub OCI descriptor graph contains a cycle")
	}
	visiting[d.Digest] = true
	defer delete(visiting, d.Digest)
	data, blob, err := c.readBlob(d)
	if err != nil {
		return err
	}
	entry := blob
	if err := c.registerBlob(d, blob); err != nil {
		return err
	}
	switch d.MediaType {
	case ociIndexMedia, dockerIndexMedia:
		if d.Size > maxMetadataBytes {
			return errors.New("Hub OCI index exceeds metadata size limit")
		}
		var index indexDocument
		if !uniqueJSON(data) || json.Unmarshal(data, &index) != nil || index.SchemaVersion != 2 || index.MediaType != "" && index.MediaType != d.MediaType || len(index.Manifests) == 0 || len(index.Manifests) > maxDescriptorsPerIndex {
			return errors.New("Hub OCI image index is invalid")
		}
		c.manifests[d.Digest] = entry
		for _, child := range index.Manifests {
			if child.MediaType != ociIndexMedia && child.MediaType != dockerIndexMedia && child.MediaType != ociImageMedia && child.MediaType != dockerImageMedia {
				return errors.New("Hub OCI index references an unsupported media type")
			}
			if (child.MediaType == ociImageMedia || child.MediaType == dockerImageMedia) && child.Platform != nil && !supportedPlatform(child.Platform.OS, child.Platform.Architecture) {
				return errors.New("Hub OCI image platform metadata is unsupported")
			}
			if err := c.walkDescriptor(child, depth+1, visiting, visited); err != nil {
				return err
			}
		}
	case ociImageMedia, dockerImageMedia:
		if d.Size > maxMetadataBytes {
			return errors.New("Hub OCI image manifest exceeds metadata size limit")
		}
		var manifest imageDocument
		if !uniqueJSON(data) || json.Unmarshal(data, &manifest) != nil || manifest.SchemaVersion != 2 || manifest.MediaType != "" && manifest.MediaType != d.MediaType || len(manifest.Layers) == 0 || len(manifest.Layers) > maxDescriptorsPerIndex {
			return errors.New("Hub OCI image manifest is invalid")
		}
		if manifest.Config.MediaType != ociConfigMedia && manifest.Config.MediaType != dockerConfigMedia {
			return errors.New("Hub OCI image configuration media type is unsupported")
		}
		if err := c.countDescriptor(); err != nil {
			return err
		}
		config, configEntry, err := c.readBlob(manifest.Config)
		if err != nil || len(config) > maxMetadataBytes {
			return errors.New("Hub OCI image configuration is unavailable")
		}
		if err := c.registerBlob(manifest.Config, configEntry); err != nil {
			return err
		}
		var image imageConfiguration
		if !uniqueJSON(config) || json.Unmarshal(config, &image) != nil || !supportedPlatform(image.OS, image.Architecture) {
			return errors.New("Hub OCI image configuration has unsupported platform metadata")
		}
		if d.Platform != nil && (d.Platform.OS != image.OS || d.Platform.Architecture != image.Architecture) {
			return errors.New("Hub OCI image descriptor and configuration platforms disagree")
		}
		if image.Config.Labels["org.opencontainers.image.version"] != c.manifest.Version ||
			image.Config.Labels["org.opencontainers.image.revision"] != c.manifest.SourceRevision {
			return errors.New("Hub OCI image version or source revision does not match the catalog")
		}
		platform := image.OS + "/" + image.Architecture
		if previous, exists := c.descriptorPlatforms[d.Digest]; exists && previous != platform {
			return errors.New("Hub OCI image descriptor has conflicting platform metadata")
		}
		c.descriptorPlatforms[d.Digest] = platform
		if c.platforms[platform] {
			return errors.New("Hub OCI image contains a duplicate platform")
		}
		c.platforms[platform] = true
		c.manifests[d.Digest] = entry
		for _, layer := range manifest.Layers {
			if !distributableLayer(layer.MediaType) {
				return errors.New("Hub OCI image uses an unsupported or nondistributable layer")
			}
			if err := c.countDescriptor(); err != nil {
				return err
			}
			_, layerEntry, err := c.readBlob(layer)
			if err != nil {
				return err
			}
			if err := c.registerBlob(layer, layerEntry); err != nil {
				return err
			}
		}
	default:
		return errors.New("Hub OCI image uses an unsupported media type")
	}
	visited[d.Digest] = true
	return nil
}

func (c *Catalog) countDescriptor() error {
	c.descriptorCount++
	if c.descriptorCount > maxTotalDescriptors {
		return errors.New("Hub OCI descriptor graph exceeds count limit")
	}
	return nil
}

func (c *Catalog) registerBlob(d descriptor, entry fileEntry) error {
	if entry, exists := c.blobs[d.Digest]; exists {
		if entry.mediaType != d.MediaType || entry.size != d.Size {
			return errors.New("Hub OCI digest has conflicting media type or size descriptors")
		}
		return nil
	}
	entry.mediaType = d.MediaType
	entry.size = d.Size
	c.blobs[d.Digest] = entry
	return nil
}

func (c *Catalog) readBlob(d descriptor) ([]byte, fileEntry, error) {
	if !digestPattern.MatchString(d.Digest) || d.Size < 0 || d.Size > maxBlobSize || len(d.URLs) != 0 {
		return nil, fileEntry{}, errors.New("Hub OCI blob descriptor is invalid or contains a foreign URL")
	}
	path := "oci/blobs/sha256/" + strings.TrimPrefix(d.Digest, "sha256:")
	file, info, err := openRootRegular(c.rootFS, path)
	if err != nil {
		return nil, fileEntry{}, errors.New("Hub OCI blob is unavailable")
	}
	defer file.Close()
	if err != nil || info.Size() != d.Size {
		return nil, fileEntry{}, errors.New("Hub OCI blob size does not match its descriptor")
	}
	if c.totalBytes > maxTotalBytes-d.Size {
		return nil, fileEntry{}, errors.New("Hub OCI layout exceeds total size limit")
	}
	c.totalBytes += d.Size
	dataLimit := int64(maxMetadataBytes)
	if d.MediaType != ociIndexMedia && d.MediaType != dockerIndexMedia && d.MediaType != ociImageMedia && d.MediaType != dockerImageMedia &&
		d.MediaType != ociConfigMedia && d.MediaType != dockerConfigMedia {
		dataLimit = 0
	}
	var data []byte
	if dataLimit > 0 {
		if d.Size > dataLimit {
			return nil, fileEntry{}, errors.New("Hub OCI metadata exceeds size limit")
		}
		data, err = io.ReadAll(io.LimitReader(file, dataLimit+1))
		if err != nil || int64(len(data)) != d.Size {
			return nil, fileEntry{}, errors.New("Hub OCI metadata is unreadable")
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, fileEntry{}, errors.New("Hub OCI blob is unreadable")
		}
	}
	if err := verifyDigestReader(file, d.Digest, d.Size); err != nil {
		return nil, fileEntry{}, err
	}
	after, err := file.Stat()
	if err != nil || !sameSnapshot(info, after) {
		return nil, fileEntry{}, errors.New("Hub OCI blob changed during validation")
	}
	return data, fileEntry{path: path, mediaType: d.MediaType, size: d.Size, digest: d.Digest, identity: info}, nil
}

func verifyDigestReader(reader io.Reader, expected string, size int64) error {
	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(reader, size+1))
	if err != nil || read != size || "sha256:"+fmt.Sprintf("%x", hash.Sum(nil)) != expected {
		return errors.New("Hub OCI blob digest does not match its descriptor")
	}
	return nil
}

func supportedPlatform(osName, architecture string) bool {
	return osName == "linux" && (architecture == "amd64" || architecture == "arm64")
}

func distributableLayer(mediaType string) bool {
	switch mediaType {
	case "application/vnd.oci.image.layer.v1.tar", "application/vnd.oci.image.layer.v1.tar+gzip",
		"application/vnd.oci.image.layer.v1.tar+zstd", "application/vnd.docker.image.rootfs.diff.tar.gzip",
		"application/vnd.docker.image.rootfs.diff.tar":
		return true
	default:
		return false
	}
}

func descriptorMediaType(mediaType string) bool {
	return mediaType == ociIndexMedia || mediaType == dockerIndexMedia || mediaType == ociImageMedia || mediaType == dockerImageMedia || mediaType == ociConfigMedia || mediaType == dockerConfigMedia || distributableLayer(mediaType)
}

func (c *Catalog) validateDescriptor(d descriptor) error {
	if !descriptorMediaType(d.MediaType) || d.Digest == "" {
		return errors.New("Hub OCI descriptor media type is unsupported")
	}
	return nil
}
