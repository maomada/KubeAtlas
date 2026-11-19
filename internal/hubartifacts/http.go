package hubartifacts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (c *Catalog) ServeHTTP(w http.ResponseWriter, r *http.Request) bool {
	if c == nil || !(r.URL.Path == "/v2/" || strings.HasPrefix(r.URL.Path, "/v2/") || strings.HasPrefix(r.URL.Path, "/downloads/agent/")) {
		return false
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		c.writeError(w, http.StatusBadRequest, "invalid_parameters", "Query and escaped paths are not supported")
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		c.writeError(w, http.StatusMethodNotAllowed, "unsupported", "Only GET and HEAD are supported")
		return true
	}
	w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if r.URL.Path == "/v2/" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte("{}\n"))
		}
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/downloads/agent/") {
		c.serveDownload(w, r)
		return true
	}
	c.serveOCI(w, r)
	return true
}

func (c *Catalog) serveDownload(w http.ResponseWriter, r *http.Request) {
	prefix := "/downloads/agent/" + c.version + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		c.writeError(w, http.StatusNotFound, "not_found", "Artifact not found")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, prefix)
	var entry fileEntry
	var contentType, digest string
	switch name {
	case "installer.sh":
		entry, contentType = c.files[c.installPath], "text/x-shellscript; charset=utf-8"
	case "bootstrap.sh":
		if c.bootstrapPath == "" {
			c.writeError(w, http.StatusNotFound, "not_found", "Artifact not found")
			return
		}
		entry, contentType = c.files[c.bootstrapPath], "text/x-shellscript; charset=utf-8"
	case "chart.tgz":
		entry, contentType = c.files[c.chartPath], "application/gzip"
	case openCostChartName:
		var ok bool
		entry, ok = c.files[openCostChartName]
		if !ok {
			c.writeError(w, http.StatusNotFound, "not_found", "Artifact not found")
			return
		}
		contentType = entry.mediaType
	case "manifest.json":
		entry, contentType = c.files[c.manifestPath], "application/json"
	case "image-manifest.json":
		digest = c.manifest.Image.Digest
		var ok bool
		entry, ok = c.agentOCI.manifests[digest]
		if !ok {
			c.writeError(w, http.StatusNotFound, "not_found", "Artifact not found")
			return
		}
		contentType = entry.mediaType
	case "checksums.txt":
		body := fmt.Sprintf("%s  installer.sh\n%s  chart.tgz\n%s  image-manifest.json\n", c.manifest.Installer.SHA256, c.manifest.Chart.SHA256, strings.TrimPrefix(c.manifest.Image.Digest, "sha256:"))
		if c.manifest.Bootstrap != nil {
			body += fmt.Sprintf("%s  bootstrap.sh\n", c.manifest.Bootstrap.SHA256)
		}
		if c.manifest.OpenCost != nil {
			body += fmt.Sprintf("%s  %s\n", c.manifest.OpenCost.ChartSHA256, openCostChartName)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="checksums.txt"`)
		http.ServeContent(w, r, "checksums.txt", time.Time{}, strings.NewReader(body))
		return
	default:
		c.writeError(w, http.StatusNotFound, "not_found", "Artifact not found")
		return
	}
	c.serveFile(w, r, entry, digest, contentType, name)
}

func (c *Catalog) serveOCI(w http.ResponseWriter, r *http.Request) {
	path, ok := strings.CutPrefix(r.URL.Path, "/v2/kubeatlas/")
	repository, location, _ := strings.Cut(path, "/")
	kind, digest, _ := strings.Cut(location, "/")
	var active, previous map[string]fileEntry
	switch repository {
	case "agent":
		active = c.agentOCI.entries(kind)
		if c.previous != nil {
			previous = c.previous.agentOCI.entries(kind)
		}
	case "opencost":
		active = c.openCostOCI.entries(kind)
		if c.previous != nil {
			previous = c.previous.openCostOCI.entries(kind)
		}
	}
	if !ok || (active == nil && previous == nil) {
		c.writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "Repository not found")
		return
	}
	entry, exists := active[digest]
	source := c
	if !exists {
		if entry, exists = previous[digest]; exists {
			source = c.previous
		}
	}
	if !digestPattern.MatchString(digest) || !exists {
		code := "BLOB_UNKNOWN"
		if kind == "manifests" {
			code = "MANIFEST_UNKNOWN"
		}
		c.writeOCIError(w, http.StatusNotFound, code, "Content not found")
		return
	}
	contentType := "application/octet-stream"
	if kind == "manifests" {
		contentType = entry.mediaType
	}
	source.serveFile(w, r, entry, digest, contentType, "")
}

func (r *ociRepository) entries(kind string) map[string]fileEntry {
	if r == nil {
		return nil
	}
	switch kind {
	case "manifests":
		return r.manifests
	case "blobs":
		return r.blobs
	default:
		return nil
	}
}

func (c *Catalog) serveFile(w http.ResponseWriter, r *http.Request, entry fileEntry, digest, contentType, downloadName string) {
	if !c.rootCurrent() {
		c.artifactUnavailable(w)
		return
	}
	file, info, err := openRootRegular(c.rootFS, entry.path)
	if err != nil {
		c.artifactUnavailable(w)
		return
	}
	defer file.Close()
	if !sameSnapshot(entry.identity, info) || info.Size() != entry.size || !c.rootCurrent() {
		c.artifactUnavailable(w)
		return
	}
	w.Header().Set("Content-Type", contentType)
	if downloadName != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+downloadName+`"`)
	}
	if digest != "" {
		w.Header().Set("Docker-Content-Digest", digest)
	}
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, filepath.Base(entry.path), info.ModTime(), file)
}

func (c *Catalog) rootCurrent() bool {
	for _, snapshot := range c.rootAncestors {
		pathInfo, err := os.Lstat(snapshot.path)
		if err != nil || !pathInfo.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(snapshot.info, pathInfo) {
			return false
		}
	}
	pathInfo, err := os.Lstat(c.root)
	if err != nil || !pathInfo.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(c.rootInfo, pathInfo) {
		return false
	}
	rootInfo, err := c.rootFS.Stat(".")
	return err == nil && os.SameFile(c.rootInfo, rootInfo)
}

func (c *Catalog) artifactUnavailable(w http.ResponseWriter) {
	w.Header().Del("Docker-Content-Digest")
	w.Header().Del("Content-Disposition")
	w.Header().Del("Content-Type")
	w.Header().Del("Accept-Ranges")
	w.Header().Set("Cache-Control", "no-store")
	c.writeError(w, http.StatusNotFound, "not_found", "Artifact not found")
}

func (c *Catalog) writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status != http.StatusMethodNotAllowed {
		_ = json.NewEncoder(w).Encode(map[string]string{"error_code": code, "error": message})
	}
}

func (c *Catalog) writeOCIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status != http.StatusMethodNotAllowed {
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"code": code, "message": message}}})
	}
}
