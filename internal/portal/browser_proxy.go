package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maomada/KubeAtlas/internal/hub"
)

const (
	maxBrowserBodyBytes = 8 << 20
	browserRequestLimit = 60 * time.Second
	maxBrowserPathBytes = 2048
)

var hubPagePaths = map[string]struct{}{
	"/clusters": {}, "/install": {}, "/agents": {}, "/issues": {}, "/investigations": {},
	"/applications": {}, "/alerts": {}, "/gitops": {}, "/certs": {}, "/checks": {},
	"/resources": {}, "/topology": {}, "/traffic": {}, "/cost": {}, "/capacity": {},
	"/timeline": {}, "/helm": {}, "/settings": {}, "/settings/sessions": {},
	"/settings/security": {}, "/settings/organization": {},
	"/settings/organization/members": {}, "/settings/organization/agent-access": {},
	"/settings/organization/enrollment-keys": {}, "/settings/organization/activity": {},
	"/settings/organization/issues": {}, "/settings/organization/checks": {},
	"/settings/organization/sso": {}, "/settings/organization/notifications": {},
	"/settings/organization/integrations": {},
}

func isHubPageRoute(r *http.Request) bool {
	if r.URL.RawPath != "" {
		return false
	}
	path := r.URL.Path
	if _, ok := hubPagePaths[path]; ok {
		return true
	}
	if !strings.HasPrefix(path, "/c/") || !canonicalPortalPath(path) {
		return false
	}
	parts := strings.Split(path, "/")
	if len(parts) < 3 || !clusterIDPattern.MatchString(parts[2]) {
		return false
	}
	if len(parts) == 3 || len(parts) == 4 && parts[3] == "" {
		return true
	}
	return parts[3] != "api" && parts[3] != "assets"
}

func canonicalPortalPath(path string) bool {
	if len(path) > maxBrowserPathBytes || strings.Contains(path, "//") ||
		strings.ContainsAny(path, "\\\x00\r\n") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func isClusterAPICandidate(path string) bool {
	parts := strings.Split(path, "/")
	return len(parts) >= 4 && parts[1] == "c" && parts[3] == "api"
}

func (p *portalHub) serveBrowserAPI(handler *Handler, w http.ResponseWriter, r *http.Request) {
	if !p.policy.allowsBrowser(r) {
		hubHTTPError(w, http.StatusForbidden, "permission_denied", "Host or browser origin is not allowed")
		return
	}
	if len(r.Header.Values("Authorization")) != 0 {
		hubHTTPError(w, http.StatusUnauthorized, "authentication_required", "Authorization headers are not accepted on browser routes")
		return
	}
	ctx, cancel, ok := handler.viewerSessionContext(r)
	if !ok {
		hubHTTPError(w, http.StatusUnauthorized, "authentication_required", "A Portal login session is required")
		return
	}
	defer cancel()

	clusterID, sourcePath, ok := browserClusterPath(r)
	if !ok {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Cluster API path is not canonical")
		return
	}
	cluster, exists := p.store.Cluster(clusterID)
	if !exists || cluster.RevokedAt != nil {
		hubHTTPError(w, http.StatusNotFound, "cluster_not_found", "Cluster is unavailable")
		return
	}
	if sourcePath == "/api/settings" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			hubHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]string{"preferenceStorage": "browser"})
		}
		return
	}

	principal := hub.Principal{UserID: p.userID, Groups: p.groups, ClusterIDs: []string{clusterID}}
	client, err := p.transport.BrowserHTTPClient(clusterID, principal)
	if err != nil {
		writeBrowserTransportError(w, err)
		return
	}
	streaming := browserStreamingPath(sourcePath) || isBrowserUpgrade(r)
	requestContext := ctx
	var requestCancel context.CancelFunc
	if !streaming {
		requestContext, requestCancel = context.WithTimeout(ctx, browserRequestLimit)
		defer requestCancel()
	}
	request := r.Clone(requestContext)
	if streaming && request.Body != nil && request.Body != http.NoBody {
		hubHTTPError(w, http.StatusBadRequest, "invalid_request", "Streaming requests cannot include a body")
		return
	}
	if !readBrowserRequestBody(w, request) {
		return
	}
	stripPortalProxyHeaders(request.Header)
	request.Header.Set("X-Request-ID", w.Header().Get("X-Request-ID"))
	target := &url.URL{Scheme: "http", Host: "localhost"}
	proxy := &httputil.ReverseProxy{
		Director: func(out *http.Request) {
			out.URL.Scheme = target.Scheme
			out.URL.Host = target.Host
			out.URL.Path = sourcePath
			out.URL.RawPath = ""
			out.Host = target.Host
			out.RequestURI = ""
		},
		Transport:     client.Transport,
		FlushInterval: -1,
		ModifyResponse: func(response *http.Response) error {
			response.Header.Del("Set-Cookie")
			response.Header.Del("Set-Cookie2")
			response.Header.Set("X-Request-ID", w.Header().Get("X-Request-ID"))
			if response.StatusCode >= 500 {
				_ = response.Body.Close()
				body, _ := json.Marshal(map[string]any{
					"error_code": "source_error", "message": "Source returned an error",
					"requestId": w.Header().Get("X-Request-ID"), "retryable": false, "outcome": "unknown",
				})
				response.Body = io.NopCloser(bytes.NewReader(body))
				response.ContentLength = int64(len(body))
				response.Header.Del("Transfer-Encoding")
				response.Header.Set("Content-Length", strconv.Itoa(len(body)))
				response.Header.Set("Content-Type", "application/json")
			}
			return nil
		},
		ErrorHandler: func(writer http.ResponseWriter, request *http.Request, err error) {
			writeBrowserTransportError(writer, err)
		},
	}
	proxy.ServeHTTP(w, request)
}

func browserClusterPath(r *http.Request) (string, string, bool) {
	if r.URL.RawPath != "" || r.URL.Fragment != "" || r.URL.ForceQuery && r.URL.RawQuery == "" ||
		len(r.URL.RawQuery) > 8192 || !canonicalPortalPath(r.URL.Path) {
		return "", "", false
	}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 || parts[0] != "" || parts[1] != "c" || !clusterIDPattern.MatchString(parts[2]) || parts[3] != "api" || parts[4] == "" {
		return "", "", false
	}
	return parts[2], "/api/" + strings.Join(parts[4:], "/"), true
}

func readBrowserRequestBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	if r.ContentLength > maxBrowserBodyBytes {
		hubHTTPError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body exceeds the browser API limit")
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBrowserBodyBytes+1))
	_ = r.Body.Close()
	if err != nil {
		hubHTTPError(w, http.StatusBadRequest, "invalid_request", "Request body could not be read")
		return false
	}
	if len(body) > maxBrowserBodyBytes {
		hubHTTPError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body exceeds the browser API limit")
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return true
}

func stripPortalProxyHeaders(headers http.Header) {
	for name := range headers {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "proxy-authorization" || lower == "cookie" ||
			lower == "forwarded" || strings.HasPrefix(lower, "x-forwarded-") ||
			strings.HasPrefix(lower, "impersonate-") || strings.HasPrefix(lower, "x-auth-request-") ||
			strings.HasPrefix(lower, "x-authenticated-") || strings.HasPrefix(lower, "x-original-") ||
			lower == "remote-user" || lower == "remote-groups" || lower == "x-user" ||
			lower == "x-user-id" || lower == "x-groups" || lower == "x-group" ||
			lower == "x-role" || lower == "x-roles" || lower == "x-radar-user" ||
			lower == "x-radar-groups" || lower == "x-radar-role" || lower == "x-radar-roles" {
			delete(headers, name)
		}
	}
}

func isBrowserUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

func browserStreamingPath(path string) bool {
	return strings.HasSuffix(path, "/stream") &&
		(path == "/api/events/stream" || path == "/api/traffic/flows/stream" ||
			strings.HasPrefix(path, "/api/diagnose/runs/") ||
			strings.HasPrefix(path, "/api/pods/") ||
			strings.HasPrefix(path, "/api/workloads/") ||
			strings.HasPrefix(path, "/api/cnpg/"))
}

func writeBrowserTransportError(w http.ResponseWriter, err error) {
	var transportError *hub.Error
	if !errors.As(err, &transportError) {
		hubHTTPError(w, http.StatusBadGateway, "source_disconnected", "Source request failed")
		return
	}
	status := http.StatusBadGateway
	switch transportError.Code {
	case hub.CodeInvalidParameters:
		status = http.StatusBadRequest
	case hub.CodePermissionDenied:
		status = http.StatusForbidden
	case hub.CodeClusterUnavailable, hub.CodeSourceChanged:
		status = http.StatusServiceUnavailable
	case hub.CodeSourceTimeout:
		status = http.StatusGatewayTimeout
	}
	outcome := "known"
	if transportError.OutcomeUnknown {
		outcome = "unknown"
	}
	hubJSON(w, status, map[string]any{
		"error_code": transportError.Code, "message": transportError.Error(),
		"requestId": w.Header().Get("X-Request-ID"), "retryable": false, "outcome": outcome,
	})
}
