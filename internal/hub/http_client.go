package hub

import (
	"bufio"
	"context"
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

	"github.com/hashicorp/yamux"
)

func (h *Hub) HTTPClient(clusterID string, principal Principal) (*http.Client, error) {
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
	transport := &tunnelRoundTripper{hub: h, state: state, clusterID: clusterID, principal: principal}
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
	if err := validateMCPRequest(request, t.principal.ReadOnly); err != nil {
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
	response, err := readSourceResponse(stream, out)
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
	if response.Body == http.NoBody {
		stop()
		lease.close()
		return response, nil
	}
	response.Body = &tunnelResponseBody{body: response.Body, lease: lease, stop: stop, ctx: request.Context()}
	return response, nil
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
			lower == "proxy-connection", lower == "keep-alive", lower == "connection",
			lower == "upgrade", lower == "expect", lower == "host", connectionSpecific:
			delete(headers, name)
		}
	}
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
