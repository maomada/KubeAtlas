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
	var path, contentType, digest string
	switch name {
	case "installer.sh":
		path, contentType = c.installPath, "text/x-shellscript; charset=utf-8"
	case "chart.tgz":
		path, contentType = c.chartPath, "application/gzip"
	case "manifest.json":
		path, contentType = c.manifestPath, "application/json"
	case "image-manifest.json":
		digest = c.manifest.Image.Digest
		entry, ok := c.manifests[digest]
		if !ok {
			c.writeError(w, http.StatusNotFound, "not_found", "Artifact not found")
			return
		}
		path, contentType = entry.path, entry.mediaType
	case "checksums.txt":
		body := fmt.Sprintf("%s  installer.sh\n%s  chart.tgz\n%s  image-manifest.json\n", c.manifest.Installer.SHA256, c.manifest.Chart.SHA256, strings.TrimPrefix(c.manifest.Image.Digest, "sha256:"))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="checksums.txt"`)
		http.ServeContent(w, r, "checksums.txt", time.Time{}, strings.NewReader(body))
		return
	default:
		c.writeError(w, http.StatusNotFound, "not_found", "Artifact not found")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	c.serveFile(w, r, path, digest)
}

func (c *Catalog) serveOCI(w http.ResponseWriter, r *http.Request) {
	const manifestPrefix = "/v2/kubeatlas/agent/manifests/"
	const blobPrefix = "/v2/kubeatlas/agent/blobs/"
	var entry fileEntry
	var digest string
	var ok bool
	manifest := false
	switch {
	case strings.HasPrefix(r.URL.Path, manifestPrefix):
		digest = strings.TrimPrefix(r.URL.Path, manifestPrefix)
		entry, ok = c.manifests[digest]
		manifest = true
	case strings.HasPrefix(r.URL.Path, blobPrefix):
		digest = strings.TrimPrefix(r.URL.Path, blobPrefix)
		entry, ok = c.blobs[digest]
	default:
		c.writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "Repository not found")
		return
	}
	if strings.Contains(digest, "/") || !digestPattern.MatchString(digest) || !ok {
		code := "BLOB_UNKNOWN"
		if manifest {
			code = "MANIFEST_UNKNOWN"
		}
		c.writeOCIError(w, http.StatusNotFound, code, "Content not found")
		return
	}
	if manifest {
		w.Header().Set("Content-Type", entry.mediaType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	c.serveFile(w, r, entry.path, digest)
}

func (c *Catalog) serveFile(w http.ResponseWriter, r *http.Request, path, digest string) {
	relative, err := filepath.Rel(c.root, path)
	if err != nil || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
		c.writeError(w, http.StatusNotFound, "not_found", "Artifact not found")
		return
	}
	path, err = regularPath(c.root, filepath.ToSlash(relative))
	if err != nil {
		c.writeError(w, http.StatusNotFound, "not_found", "Artifact not found")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		c.writeError(w, http.StatusNotFound, "not_found", "Artifact not found")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		c.writeError(w, http.StatusNotFound, "not_found", "Artifact not found")
		return
	}
	if digest != "" {
		w.Header().Set("Docker-Content-Digest", digest)
	}
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
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
