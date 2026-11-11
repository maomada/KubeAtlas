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
	ociIndexMedia     = "application/vnd.oci.image.index.v1+json"
	dockerIndexMedia  = "application/vnd.docker.distribution.manifest.list.v2+json"
	ociImageMedia     = "application/vnd.oci.image.manifest.v1+json"
	dockerImageMedia  = "application/vnd.docker.distribution.manifest.v2+json"
	ociConfigMedia    = "application/vnd.oci.image.config.v1+json"
	dockerConfigMedia = "application/vnd.docker.container.image.v1+json"
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
	layoutFile, err := regularPath(layoutPath, "oci-layout")
	if err != nil {
		return errors.New("Hub OCI layout metadata is unavailable")
	}
	layoutBytes, err := readBounded(layoutFile, 64<<10)
	if err != nil || !uniqueJSON(layoutBytes) {
		return errors.New("Hub OCI layout metadata is invalid")
	}
	var layout struct {
		Version string `json:"imageLayoutVersion"`
	}
	if json.Unmarshal(layoutBytes, &layout) != nil || layout.Version != "1.0.0" {
		return errors.New("Hub OCI layout version is unsupported")
	}
	indexPath, err := regularPath(layoutPath, "index.json")
	if err != nil {
		return errors.New("Hub OCI index is unavailable")
	}
	indexBytes, err := readBounded(indexPath, maxMetadataBytes)
	if err != nil || !uniqueJSON(indexBytes) {
		return errors.New("Hub OCI index is invalid")
	}
	var root indexDocument
	if json.Unmarshal(indexBytes, &root) != nil || root.SchemaVersion != 2 || len(root.Manifests) == 0 ||
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
	if selected.MediaType != ociIndexMedia && selected.MediaType != dockerIndexMedia {
		return errors.New("Hub OCI Agent image must be a multi-platform image index")
	}
	c.layoutRoot = layoutPath
	c.totalBytes = 0
	if err := c.walkDescriptor(*selected, map[string]bool{}, map[string]bool{}); err != nil {
		return err
	}
	if len(c.platforms) == 0 {
		return errors.New("Hub OCI image has no supported Linux platform")
	}
	if selected.MediaType == ociIndexMedia || selected.MediaType == dockerIndexMedia {
		if !c.platforms["linux/amd64"] || !c.platforms["linux/arm64"] {
			return errors.New("Hub OCI index must include Linux amd64 and arm64 images")
		}
	}
	return nil
}

func (c *Catalog) walkDescriptor(d descriptor, visiting, visited map[string]bool) error {
	if !descriptorMediaType(d.MediaType) || !digestPattern.MatchString(d.Digest) || d.Size < 0 || d.Size > maxBlobSize || len(d.URLs) != 0 {
		return errors.New("Hub OCI descriptor is invalid or contains a foreign URL")
	}
	if visited[d.Digest] {
		entry, ok := c.blobs[d.Digest]
		if !ok || entry.mediaType != d.MediaType || entry.size != d.Size {
			return errors.New("Hub OCI descriptor has conflicting media type or size")
		}
		return nil
	}
	if visiting[d.Digest] {
		return errors.New("Hub OCI descriptor graph contains a cycle")
	}
	visiting[d.Digest] = true
	defer delete(visiting, d.Digest)
	data, path, err := c.readBlob(d)
	if err != nil {
		return err
	}
	entry := fileEntry{path: path, mediaType: d.MediaType, size: d.Size}
	c.blobs[d.Digest] = entry
	switch d.MediaType {
	case ociIndexMedia, dockerIndexMedia:
		if d.Size > maxMetadataBytes {
			return errors.New("Hub OCI index exceeds metadata size limit")
		}
		var index indexDocument
		if !uniqueJSON(data) || json.Unmarshal(data, &index) != nil || index.SchemaVersion != 2 || index.MediaType != "" && index.MediaType != d.MediaType || len(index.Manifests) == 0 {
			return errors.New("Hub OCI image index is invalid")
		}
		c.manifests[d.Digest] = entry
		for _, child := range index.Manifests {
			if child.MediaType != ociIndexMedia && child.MediaType != dockerIndexMedia && child.MediaType != ociImageMedia && child.MediaType != dockerImageMedia {
				return errors.New("Hub OCI index references an unsupported media type")
			}
			if child.MediaType == ociImageMedia || child.MediaType == dockerImageMedia {
				if child.Platform == nil || !supportedPlatform(child.Platform.OS, child.Platform.Architecture) {
					return errors.New("Hub OCI image platform metadata is missing or unsupported")
				}
			}
			if err := c.walkDescriptor(child, visiting, visited); err != nil {
				return err
			}
		}
	case ociImageMedia, dockerImageMedia:
		if d.Size > maxMetadataBytes {
			return errors.New("Hub OCI image manifest exceeds metadata size limit")
		}
		var manifest imageDocument
		if !uniqueJSON(data) || json.Unmarshal(data, &manifest) != nil || manifest.SchemaVersion != 2 || manifest.MediaType != "" && manifest.MediaType != d.MediaType || len(manifest.Layers) == 0 {
			return errors.New("Hub OCI image manifest is invalid")
		}
		if manifest.Config.MediaType != ociConfigMedia && manifest.Config.MediaType != dockerConfigMedia {
			return errors.New("Hub OCI image configuration media type is unsupported")
		}
		config, configPath, err := c.readBlob(manifest.Config)
		if err != nil || len(config) > maxMetadataBytes {
			return errors.New("Hub OCI image configuration is unavailable")
		}
		c.blobs[manifest.Config.Digest] = fileEntry{path: configPath, mediaType: manifest.Config.MediaType, size: manifest.Config.Size}
		var image imageConfiguration
		if !uniqueJSON(config) || json.Unmarshal(config, &image) != nil || !supportedPlatform(image.OS, image.Architecture) {
			return errors.New("Hub OCI image configuration has unsupported platform metadata")
		}
		if d.Platform != nil && (d.Platform.OS != image.OS || d.Platform.Architecture != image.Architecture) {
			return errors.New("Hub OCI image descriptor and configuration platforms disagree")
		}
		if !dPlatformValid(d, image.OS, image.Architecture) {
			return errors.New("Hub OCI image platform metadata is missing")
		}
		if image.Config.Labels["org.opencontainers.image.version"] != c.manifest.Version ||
			image.Config.Labels["org.opencontainers.image.revision"] != c.manifest.SourceRevision {
			return errors.New("Hub OCI image version or source revision does not match the catalog")
		}
		platform := image.OS + "/" + image.Architecture
		if c.platforms[platform] {
			return errors.New("Hub OCI image contains a duplicate platform")
		}
		c.platforms[platform] = true
		c.manifests[d.Digest] = entry
		for _, layer := range manifest.Layers {
			if !distributableLayer(layer.MediaType) {
				return errors.New("Hub OCI image uses an unsupported or nondistributable layer")
			}
			_, layerPath, err := c.readBlob(layer)
			if err != nil {
				return err
			}
			c.blobs[layer.Digest] = fileEntry{path: layerPath, mediaType: layer.MediaType, size: layer.Size}
		}
	default:
		return errors.New("Hub OCI image uses an unsupported media type")
	}
	visited[d.Digest] = true
	return nil
}

func (c *Catalog) readBlob(d descriptor) ([]byte, string, error) {
	if !digestPattern.MatchString(d.Digest) || d.Size < 0 || d.Size > maxBlobSize || len(d.URLs) != 0 {
		return nil, "", errors.New("Hub OCI blob descriptor is invalid or contains a foreign URL")
	}
	name := "blobs/sha256/" + strings.TrimPrefix(d.Digest, "sha256:")
	path, err := regularPath(c.layoutRoot, name)
	if err != nil {
		return nil, "", errors.New("Hub OCI blob is missing or escapes the layout")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", errors.New("Hub OCI blob is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != d.Size {
		return nil, "", errors.New("Hub OCI blob size does not match its descriptor")
	}
	if c.totalBytes > maxTotalBytes-d.Size {
		return nil, "", errors.New("Hub OCI layout exceeds total size limit")
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
			return nil, "", errors.New("Hub OCI metadata exceeds size limit")
		}
		data, err = io.ReadAll(io.LimitReader(file, dataLimit+1))
		if err != nil || int64(len(data)) != d.Size {
			return nil, "", errors.New("Hub OCI metadata is unreadable")
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, "", errors.New("Hub OCI blob is unreadable")
		}
	}
	if err := verifyDigestReader(file, d.Digest, d.Size); err != nil {
		return nil, "", err
	}
	return data, path, nil
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

func dPlatformValid(d descriptor, osName, architecture string) bool {
	return d.Platform != nil && supportedPlatform(d.Platform.OS, d.Platform.Architecture) &&
		d.Platform.OS == osName && d.Platform.Architecture == architecture
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
