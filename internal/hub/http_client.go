package hub

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"math"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

func (h *Hub) HTTPClient(clusterID string, principal Principal) (*http.Client, error) {
	return h.httpClient(clusterID, principal, false)
}

// BrowserHTTPClient creates a cluster-scoped client for the Portal's live API
// proxy. It deliberately uses a separate route policy from the MCP client.
func (h *Hub) BrowserHTTPClient(clusterID string, principal Principal) (*http.Client, error) {
	return h.httpClient(clusterID, principal, true)
}

func (h *Hub) httpClient(clusterID string, principal Principal, browser bool) (*http.Client, error) {
	if !validClusterID(clusterID) {
		return nil, tunnelError(CodeInvalidParameters, false, nil)
	}
	if !principalAllowed(principal, clusterID) {
		return nil, tunnelError(CodePermissionDenied, false, nil)
	}
	h.mu.RLock()
	state := h.agents[clusterID]
	closed := h.closed
	h.mu.RUnlock()
	if closed || state == nil {
		return nil, tunnelError(CodeClusterUnavailable, false, nil)
	}
	if _, active := state.currentSession(); !active {
		return nil, tunnelError(CodeClusterUnavailable, false, nil)
	}
	principal.Groups = append([]string(nil), principal.Groups...)
	principal.ClusterIDs = append([]string(nil), principal.ClusterIDs...)
	transport := &tunnelRoundTripper{hub: h, state: state, clusterID: clusterID, principal: principal, browser: browser}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return tunnelError(CodePermissionDenied, false, nil)
		},
	}, nil
}

type tunnelRoundTripper struct {
	hub       *Hub
	state     *agentState
	clusterID string
	principal Principal
	browser   bool
}

func (t *tunnelRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	var requestBody *requestBodyCloser
	if request != nil && request.Body != nil {
		requestBody = newRequestBodyCloser(request.Body)
		defer func() {
			if request.Context().Err() != nil {
				requestBody.CloseAsync()
				return
			}
			_ = requestBody.Close()
		}()
	}
	if request == nil || request.URL == nil {
		return nil, tunnelError(CodeInvalidParameters, false, nil)
	}
	if err := request.Context().Err(); err != nil {
		return nil, contextTunnelError(request.Context(), false)
	}
	if t.browser {
		if err := validateBrowserRequest(request, t.principal.ReadOnly); err != nil {
			return nil, err
		}
	} else if err := validateMCPRequest(request, t.principal.ReadOnly); err != nil {
		return nil, err
	}
	if !t.hub.isCurrent(t.state) {
		return nil, tunnelError(CodeSourceChanged, false, nil)
	}
	session, active := t.state.currentSession()
	if !active {
		return nil, t.connectionError()
	}
	select {
	case t.state.slots <- struct{}{}:
	case <-request.Context().Done():
		return nil, contextTunnelError(request.Context(), false)
	case <-t.state.done:
		return nil, t.connectionError()
	case <-session.CloseChan():
		return nil, t.connectionError()
	}
	var releaseOnce sync.Once
	releaseSlot := func() {
		releaseOnce.Do(func() {
			<-t.state.slots
		})
	}
	if err := request.Context().Err(); err != nil {
		releaseSlot()
		return nil, contextTunnelError(request.Context(), false)
	}
	if !t.hub.isCurrent(t.state) {
		releaseSlot()
		return nil, tunnelError(CodeSourceChanged, false, nil)
	}
	current, active := t.state.currentSession()
	if !active || current != session || session.IsClosed() {
		releaseSlot()
		return nil, t.connectionError()
	}
	stream, err := t.openStreamCancelable(request.Context(), session, releaseSlot)
	if err != nil {
		return nil, err
	}
	lease := &streamLease{stream: stream, release: releaseSlot, requestBody: requestBody}
	if err := request.Context().Err(); err != nil {
		lease.close()
		return nil, contextTunnelError(request.Context(), false)
	}
	if !t.hub.isCurrent(t.state) {
		lease.close()
		return nil, tunnelError(CodeSourceChanged, false, nil)
	}
	current, active = t.state.currentSession()
	if !active || current != session || session.IsClosed() {
		lease.close()
		return nil, t.connectionError()
	}
	out := request.Clone(request.Context())
	urlCopy := *request.URL
	out.URL = &urlCopy
	out.Header = cloneHeaders(request.Header)
	if requestBody != nil {
		out.Body = requestBody
	}
	stripInboundHeaders(out.Header)
	if t.browser {
		stripBrowserHeaders(out.Header)
		if isBrowserWebSocketUpgrade(request) {
			setBrowserWebSocketHeaders(out.Header, request.Header)
		}
	}
	out.Host = "localhost"
	out.RequestURI = ""
	out.Close = true
	out.Header.Set("X-Forwarded-User", t.principal.UserID)
	if len(t.principal.Groups) > 0 {
		out.Header.Set("X-Forwarded-Groups", strings.Join(t.principal.Groups, ","))
	}
	stop := context.AfterFunc(request.Context(), lease.close)
	if err := request.Context().Err(); err != nil {
		stop()
		lease.close()
		return nil, contextTunnelError(request.Context(), false)
	}
	if !t.hub.isCurrent(t.state) {
		stop()
		lease.close()
		return nil, tunnelError(CodeSourceChanged, false, nil)
	}
	if err := out.Write(stream); err != nil {
		stop()
		lease.close()
		if request.Context().Err() != nil {
			return nil, contextTunnelError(request.Context(), true)
		}
		return nil, tunnelError(CodeSourceDisconnected, true, err)
	}
	var response *http.Response
	var reader *bufio.Reader
	upgraded := false
	if t.browser {
		response, reader, upgraded, err = readBrowserSourceResponse(stream, out, isBrowserWebSocketUpgrade(request))
	} else {
		response, err = readSourceResponse(stream, out)
	}
	if err != nil {
		stop()
		lease.close()
		if request.Context().Err() != nil {
			return nil, contextTunnelError(request.Context(), true)
		}
		return nil, tunnelError(CodeSourceDisconnected, true, err)
	}
	response.Request = request
	response.Header.Del("Set-Cookie")
	response.Header.Del("Set-Cookie2")
	if upgraded {
		response.Body = &tunnelDuplexBody{reader: reader, stream: stream, lease: lease, stop: stop, ctx: request.Context()}
		return response, nil
	}
	if response.Body == http.NoBody {
		stop()
		lease.close()
		return response, nil
	}
	response.Body = &tunnelResponseBody{body: response.Body, lease: lease, stop: stop, ctx: request.Context()}
	return response, nil
}

const (
	maxBrowserQueryBytes = 8192
	maxBrowserQueryItems = 128
	maxBrowserQueryValue = 4096
)

var browserAPIFamilies = []string{
	"/api/health", "/api/capabilities", "/api/cluster-info", "/api/dashboard",
	"/api/namespaces", "/api/api-resources", "/api/resource-counts", "/api/resources",
	"/api/secrets", "/api/certificates", "/api/audit", "/api/policy",
	"/api/upgrade-readiness", "/api/trace", "/api/packages", "/api/applications",
	"/api/search", "/api/issues", "/api/events", "/api/changes", "/api/timeline",
	"/api/pods", "/api/services", "/api/metrics", "/api/nodes", "/api/workloads",
	"/api/helm", "/api/prometheus", "/api/opencost", "/api/capacity", "/api/rbac",
	"/api/cnpg", "/api/velero", "/api/flux", "/api/rollouts", "/api/argo",
	"/api/images", "/api/network-policies", "/api/traffic", "/api/diagnose",
	"/api/gitops",
}

func validateBrowserRequest(request *http.Request, readOnly bool) error {
	u := request.URL
	if u.Scheme != "http" || u.Host != "localhost" || u.User != nil || u.Opaque != "" ||
		u.Fragment != "" || u.RawPath != "" || u.RawQuery != "" && len(u.RawQuery) > maxBrowserQueryBytes {
		return tunnelError(CodeInvalidParameters, false, nil)
	}
	if _, err := url.ParseQuery(u.RawQuery); err != nil {
		return tunnelError(CodeInvalidParameters, false, err)
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return tunnelError(CodeInvalidParameters, false, err)
	}
	if !canonicalBrowserAPIPath(u.Path) || !browserAPIPathAllowed(u.Path) {
		return tunnelError(CodePermissionDenied, false, nil)
	}
	if len(query) > maxBrowserQueryItems {
		return tunnelError(CodeInvalidParameters, false, nil)
	}
	for key, values := range query {
		if len(key) > 128 || hasASCIIControls(key) || len(values) > maxBrowserQueryItems {
			return tunnelError(CodeInvalidParameters, false, nil)
		}
		for _, value := range values {
			if len(value) > maxBrowserQueryValue || hasASCIIControls(value) {
				return tunnelError(CodeInvalidParameters, false, nil)
			}
		}
	}
	for _, key := range []string{"namespace", "namespaces", "kind", "name"} {
		for _, value := range query[key] {
			if len(value) > 2048 {
				return tunnelError(CodeInvalidParameters, false, nil)
			}
		}
	}

	webSocket := isBrowserWebSocketUpgrade(request)
	execPath := isPodExecPath(u.Path)
	hasUpgrade := hasHeaderName(request.Header, "Upgrade") ||
		hasToken(headerValuesFold(request.Header, "Connection"), "upgrade")
	if execPath {
		if request.Method != http.MethodGet || readOnly || !webSocket || !validBrowserWebSocketRequest(request) {
			return tunnelError(CodePermissionDenied, false, nil)
		}
	} else if hasUpgrade || webSocket {
		return tunnelError(CodePermissionDenied, false, nil)
	}
	if readOnly && request.Method != http.MethodGet && request.Method != http.MethodHead &&
		!(request.Method == http.MethodPost && u.Path == "/api/cluster/namespace") {
		return tunnelError(CodePermissionDenied, false, nil)
	}
	trafficPath := u.Path == "/api/traffic" || strings.HasPrefix(u.Path, "/api/traffic/")
	if trafficPath && request.Method != http.MethodGet && request.Method != http.MethodHead &&
		!(request.Method == http.MethodPost && u.Path == "/api/traffic/connect-managed") {
		return tunnelError(CodePermissionDenied, false, nil)
	}
	switch u.Path {
	case "/api/health", "/api/capabilities", "/api/cluster-info", "/api/version-check",
		"/api/namespaces", "/api/api-resources", "/api/resource-counts", "/api/certificates",
		"/api/upgrade-readiness", "/api/packages", "/api/search", "/api/events",
		"/api/changes", "/api/topology", "/api/vitals", "/api/connection",
		"/api/auth/me", "/api/agents", "/api/settings/audit", "/api/cluster/namespace-scope":
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			return tunnelError(CodePermissionDenied, false, nil)
		}
	case "/api/traffic/source", "/api/traffic/connection", "/api/traffic/sources":
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			return tunnelError(CodePermissionDenied, false, nil)
		}
	case "/api/cluster/namespace":
		if request.Method != http.MethodPost {
			return tunnelError(CodePermissionDenied, false, nil)
		}
	case "/api/traffic/connect-managed":
		if request.Method != http.MethodPost {
			return tunnelError(CodePermissionDenied, false, nil)
		}
	case "/api/gitops/write-evidence":
		if request.Method != http.MethodPost {
			return tunnelError(CodePermissionDenied, false, nil)
		}
	}
	if readOnly && u.Path == "/api/gitops/write-evidence" {
		return tunnelError(CodePermissionDenied, false, nil)
	}
	if readOnly && (u.Path == "/api/trace" || strings.HasPrefix(u.Path, "/api/trace/")) &&
		browserQueryTrue(query.Get("probe")) {
		return tunnelError(CodePermissionDenied, false, nil)
	}
	if (u.Path == "/api/connection" || strings.HasPrefix(u.Path, "/api/connection/")) &&
		u.Path != "/api/connection" {
		return tunnelError(CodePermissionDenied, false, nil)
	}
	if strings.HasPrefix(u.Path, "/api/pods/") && podFileSavePath(u.Path) {
		return tunnelError(CodePermissionDenied, false, nil)
	}
	return nil
}

func browserQueryTrue(value string) bool {
	switch strings.ToLower(value) {
	case "true", "1", "t", "yes":
		return true
	default:
		return false
	}
}

func hasASCIIControls(value string) bool {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return true
		}
	}
	return false
}

func canonicalBrowserAPIPath(path string) bool {
	if !strings.HasPrefix(path, "/api/") || strings.Contains(path, "//") ||
		strings.ContainsAny(path, "\\\x00\r\n") {
		return false
	}
	for _, char := range path {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func browserAPIPathAllowed(path string) bool {
	if path == "/api/traffic/connect" {
		return false
	}
	for _, family := range browserAPIFamilies {
		if path == family || strings.HasPrefix(path, family+"/") {
			return true
		}
	}
	return path == "/api/version-check" || path == "/api/connection" ||
		path == "/api/auth/me" || path == "/api/agents" || path == "/api/settings/audit" ||
		path == "/api/topology" || path == "/api/vitals" ||
		path == "/api/cluster/namespace-scope" || path == "/api/cluster/namespace"
}

func isPodExecPath(path string) bool {
	parts := strings.Split(path, "/")
	return len(parts) == 6 && parts[1] == "api" && parts[2] == "pods" &&
		parts[3] != "" && parts[4] != "" && parts[5] == "exec"
}

func podFileSavePath(path string) bool {
	parts := strings.Split(path, "/")
	for i := 5; i < len(parts)-1; i++ {
		if parts[i] == "files" && parts[i+1] == "save" {
			return true
		}
	}
	return false
}

func isBrowserWebSocketUpgrade(request *http.Request) bool {
	return websocket.IsWebSocketUpgrade(request)
}

func validBrowserWebSocketRequest(request *http.Request) bool {
	keys := headerValuesFold(request.Header, "Sec-WebSocket-Key")
	versions := headerValuesFold(request.Header, "Sec-WebSocket-Version")
	if len(keys) != 1 || len(versions) != 1 || strings.TrimSpace(versions[0]) != "13" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keys[0]))
	return err == nil && len(decoded) == 16
}

func setBrowserWebSocketHeaders(target, source http.Header) {
	target.Set("Connection", "Upgrade")
	target.Set("Upgrade", "websocket")
	for _, name := range []string{"Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions"} {
		for _, value := range headerValuesFold(source, name) {
			target.Add(name, value)
		}
	}
}

func stripBrowserHeaders(headers http.Header) {
	for name := range headers {
		lower := strings.ToLower(textproto.CanonicalMIMEHeaderKey(name))
		if lower == "origin" || lower == "referer" || strings.HasPrefix(lower, "sec-fetch-") ||
			strings.HasPrefix(lower, "sec-websocket-") {
			delete(headers, name)
		}
	}
}

func readBrowserSourceResponse(stream *yamux.Stream, request *http.Request, expectUpgrade bool) (*http.Response, *bufio.Reader, bool, error) {
	limited := &io.LimitedReader{R: stream, N: maxResponseHeaderBytes}
	reader := bufio.NewReader(limited)
	for interim := 0; ; interim++ {
		response, err := http.ReadResponse(reader, request)
		if err != nil {
			return nil, nil, false, err
		}
		if response.StatusCode == http.StatusSwitchingProtocols {
			if !expectUpgrade || !hasToken(headerValuesFold(response.Header, "Connection"), "upgrade") ||
				!strings.EqualFold(strings.TrimSpace(response.Header.Get("Upgrade")), "websocket") ||
				!validWebSocketAccept(request.Header, response.Header) {
				return nil, nil, false, errors.New("source response upgrade rejected")
			}
			limited.N = math.MaxInt64
			return response, reader, true, nil
		}
		if response.StatusCode >= 100 && response.StatusCode < 200 {
			if interim >= maxInterimResponses {
				return nil, nil, false, errors.New("too many interim source responses")
			}
			continue
		}
		limited.N = math.MaxInt64
		return response, reader, false, nil
	}
}

func validWebSocketAccept(request, response http.Header) bool {
	keys := headerValuesFold(request, "Sec-WebSocket-Key")
	if len(keys) != 1 {
		return false
	}
	digest := sha1.Sum([]byte(strings.TrimSpace(keys[0]) + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	expected := base64.StdEncoding.EncodeToString(digest[:])
	actual := strings.TrimSpace(response.Get("Sec-WebSocket-Accept"))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func (t *tunnelRoundTripper) openStreamCancelable(ctx context.Context, session *yamux.Session, release func()) (*yamux.Stream, error) {
	if err := ctx.Err(); err != nil {
		release()
		return nil, contextTunnelError(ctx, false)
	}
	select {
	case <-t.state.done:
		release()
		return nil, t.connectionError()
	case <-session.CloseChan():
		release()
		return nil, t.connectionError()
	default:
	}
	type result struct {
		stream *yamux.Stream
		err    error
	}
	results := make(chan result)
	go func() {
		if ctx.Err() != nil {
			release()
			return
		}
		stream, err := session.OpenStream()
		if err != nil {
			release()
			select {
			case results <- result{err: err}:
			case <-ctx.Done():
			case <-t.state.done:
			case <-session.CloseChan():
			}
			return
		}
		select {
		case results <- result{stream: stream}:
		case <-ctx.Done():
			cleanupOpenedStream(stream, nil, release)
		case <-t.state.done:
			cleanupOpenedStream(stream, nil, release)
		case <-session.CloseChan():
			cleanupOpenedStream(stream, nil, release)
		}
	}()
	select {
	case opened := <-results:
		if opened.err != nil {
			if ctx.Err() != nil {
				return nil, contextTunnelError(ctx, false)
			}
			return nil, t.connectionError()
		}
		return opened.stream, nil
	case <-ctx.Done():
		return nil, contextTunnelError(ctx, false)
	case <-t.state.done:
		return nil, t.connectionError()
	case <-session.CloseChan():
		return nil, t.connectionError()
	}
}

func (t *tunnelRoundTripper) connectionError() error {
	if !t.hub.isCurrent(t.state) {
		return tunnelError(CodeSourceChanged, false, nil)
	}
	return tunnelError(CodeSourceDisconnected, false, nil)
}

func readSourceResponse(stream *yamux.Stream, request *http.Request) (*http.Response, error) {
	limited := &io.LimitedReader{R: stream, N: maxResponseHeaderBytes}
	reader := bufio.NewReader(limited)
	for interim := 0; ; interim++ {
		response, err := http.ReadResponse(reader, request)
		if err != nil {
			return nil, err
		}
		if response.StatusCode == http.StatusSwitchingProtocols {
			return nil, errors.New("source response upgrade rejected")
		}
		if response.StatusCode >= 100 && response.StatusCode < 200 {
			if interim >= maxInterimResponses {
				return nil, errors.New("too many interim source responses")
			}
			continue
		}
		limited.N = math.MaxInt64
		return response, nil
	}
}

func (h *Hub) isCurrent(state *agentState) bool {
	h.mu.RLock()
	current := !h.closed && h.agents[state.agent.ClusterID] == state
	h.mu.RUnlock()
	return current
}

func validateMCPRequest(request *http.Request, readOnly bool) error {
	u := request.URL
	if u.Scheme != "http" || u.Host != "localhost" || u.User != nil || u.Opaque != "" ||
		u.Fragment != "" || u.RawPath != "" || u.RawQuery != "" && len(u.RawQuery) > 8192 {
		return tunnelError(CodeInvalidParameters, false, nil)
	}
	if _, err := url.ParseQuery(u.RawQuery); err != nil {
		return tunnelError(CodeInvalidParameters, false, err)
	}
	if u.Path != "/mcp" && u.Path != "/mcp-readonly" {
		return tunnelError(CodePermissionDenied, false, nil)
	}
	if readOnly && u.Path != "/mcp-readonly" {
		return tunnelError(CodePermissionDenied, false, nil)
	}
	if hasHeaderName(request.Header, "Upgrade") || hasToken(headerValuesFold(request.Header, "Connection"), "upgrade") {
		return tunnelError(CodePermissionDenied, false, nil)
	}
	return nil
}

func hasHeaderName(headers http.Header, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func headerValuesFold(headers http.Header, name string) []string {
	var values []string
	for key, entries := range headers {
		if strings.EqualFold(key, name) {
			values = append(values, entries...)
		}
	}
	return values
}

func hasToken(values []string, token string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func cloneHeaders(headers http.Header) http.Header {
	if headers == nil {
		return make(http.Header)
	}
	return headers.Clone()
}

func stripInboundHeaders(headers http.Header) {
	connectionTokens := make(map[string]struct{})
	for _, value := range headerValuesFold(headers, "Connection") {
		for _, token := range strings.Split(value, ",") {
			if token = strings.TrimSpace(token); token != "" {
				connectionTokens[strings.ToLower(token)] = struct{}{}
			}
		}
	}
	for name := range headers {
		lower := strings.ToLower(textproto.CanonicalMIMEHeaderKey(name))
		_, connectionSpecific := connectionTokens[lower]
		switch {
		case lower == "authorization", lower == "proxy-authorization", lower == "cookie",
			lower == "forwarded", lower == "x-forwarded-user", lower == "x-forwarded-groups",
			strings.HasPrefix(lower, "x-forwarded-"), strings.HasPrefix(lower, "impersonate-"),
			isInboundIdentityHeader(lower),
			lower == "proxy-connection", lower == "keep-alive", lower == "connection",
			lower == "upgrade", lower == "expect", lower == "host", connectionSpecific:
			delete(headers, name)
		}
	}
}

func isInboundIdentityHeader(name string) bool {
	switch name {
	case "remote-user", "remote-groups", "x-user", "x-user-id", "x-group", "x-groups",
		"x-role", "x-roles", "x-authenticated-user", "x-authenticated-groups",
		"x-auth-request-user", "x-auth-request-groups", "x-auth-request-email",
		"x-original-user", "x-original-groups", "x-radar-user", "x-radar-groups",
		"x-radar-role", "x-radar-roles", "x-radar-identity":
		return true
	}
	return strings.HasPrefix(name, "x-auth-request-") ||
		strings.HasPrefix(name, "x-authenticated-") ||
		strings.HasPrefix(name, "x-original-")
}

const (
	maxResponseHeaderBytes = 64 << 10
	maxInterimResponses    = 8
)

func contextTunnelError(ctx context.Context, unknown bool) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return tunnelError(CodeSourceTimeout, unknown, ctx.Err())
	}
	return tunnelError(CodeSourceDisconnected, unknown, ctx.Err())
}

type streamLease struct {
	stream      *yamux.Stream
	requestBody *requestBodyCloser
	release     func()
	once        sync.Once
}

func (l *streamLease) close() {
	l.once.Do(func() {
		go cleanupOpenedStream(l.stream, l.requestBody, l.release)
	})
}

func cleanupOpenedStream(stream *yamux.Stream, requestBody *requestBodyCloser, release func()) {
	_ = stream.SetDeadline(time.Now())
	if requestBody != nil {
		requestBody.CloseAsync()
		requestBody.wait()
	}
	_ = stream.Close()
	release()
}

type requestBodyCloser struct {
	body    io.ReadCloser
	mu      sync.Mutex
	started bool
	done    chan struct{}
}

func newRequestBodyCloser(body io.ReadCloser) *requestBodyCloser {
	return &requestBodyCloser{body: body, done: make(chan struct{})}
}

func (c *requestBodyCloser) Read(data []byte) (int, error) {
	return c.body.Read(data)
}

func (c *requestBodyCloser) Close() error {
	return c.startClose(false)
}

func (c *requestBodyCloser) CloseAsync() {
	_ = c.startClose(true)
}

func (c *requestBodyCloser) startClose(async bool) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	c.mu.Unlock()
	closeBody := func() {
		_ = c.body.Close()
		close(c.done)
	}
	if async {
		go closeBody()
		return nil
	}
	closeBody()
	return nil
}

func (c *requestBodyCloser) wait() {
	<-c.done
}

type tunnelResponseBody struct {
	body   io.ReadCloser
	lease  *streamLease
	stop   func() bool
	ctx    context.Context
	closed atomic.Bool
}

type tunnelDuplexBody struct {
	reader *bufio.Reader
	stream *yamux.Stream
	lease  *streamLease
	stop   func() bool
	ctx    context.Context
	closed atomic.Bool
}

func (b *tunnelDuplexBody) Read(data []byte) (int, error) {
	if b.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	n, err := b.reader.Read(data)
	if err == io.EOF {
		_ = b.Close()
		return n, io.EOF
	}
	if err != nil {
		_ = b.Close()
		if b.ctx.Err() != nil {
			return n, contextTunnelError(b.ctx, true)
		}
		return n, tunnelError(CodeSourceDisconnected, true, err)
	}
	return n, nil
}

func (b *tunnelDuplexBody) Write(data []byte) (int, error) {
	if b.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	n, err := b.stream.Write(data)
	if err != nil {
		_ = b.Close()
		if b.ctx.Err() != nil {
			return n, contextTunnelError(b.ctx, true)
		}
		return n, tunnelError(CodeSourceDisconnected, true, err)
	}
	return n, nil
}

func (b *tunnelDuplexBody) Close() error {
	if b.closed.CompareAndSwap(false, true) {
		b.stop()
		b.lease.close()
	}
	return nil
}

func (b *tunnelResponseBody) Read(data []byte) (int, error) {
	if b.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	n, err := b.body.Read(data)
	if err != nil {
		if err == io.EOF {
			_ = b.Close()
			return n, io.EOF
		}
		if b.ctx.Err() != nil {
			return n, contextTunnelError(b.ctx, true)
		}
		return n, tunnelError(CodeSourceDisconnected, true, err)
	}
	return n, nil
}

func (b *tunnelResponseBody) Close() error {
	if b.closed.CompareAndSwap(false, true) {
		b.stop()
		b.lease.close()
	}
	return nil
}

var _ http.RoundTripper = (*tunnelRoundTripper)(nil)
