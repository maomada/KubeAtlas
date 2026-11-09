package hub

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
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
	if request != nil && request.Body != nil {
		defer request.Body.Close()
	}
	if request == nil || request.URL == nil {
		return nil, tunnelError(CodeInvalidParameters, false, nil)
	}
	if err := validateMCPRequest(request, t.principal.ReadOnly); err != nil {
		return nil, err
	}
	if !t.hub.isCurrent(t.state) {
		return nil, tunnelError(CodeSourceChanged, false, nil)
	}
	select {
	case t.state.slots <- struct{}{}:
	case <-request.Context().Done():
		return nil, contextTunnelError(request.Context(), false)
	}
	released := false
	releaseSlot := func() {
		if !released {
			<-t.state.slots
			released = true
		}
	}
	if !t.hub.isCurrent(t.state) {
		releaseSlot()
		return nil, tunnelError(CodeSourceChanged, false, nil)
	}
	session, active := t.state.currentSession()
	if !active {
		releaseSlot()
		return nil, tunnelError(CodeSourceDisconnected, false, nil)
	}
	stream, err := session.OpenStream()
	if err != nil {
		releaseSlot()
		if request.Context().Err() != nil {
			return nil, contextTunnelError(request.Context(), false)
		}
		return nil, tunnelError(CodeSourceDisconnected, false, err)
	}
	lease := &streamLease{stream: stream, release: releaseSlot}
	out := request.Clone(request.Context())
	urlCopy := *request.URL
	out.URL = &urlCopy
	out.Header = cloneHeaders(request.Header)
	stripIdentityHeaders(out.Header)
	out.Host = "localhost"
	out.RequestURI = ""
	out.Close = true
	out.Header.Del("Host")
	out.Header.Del("Connection")
	out.Header.Del("Upgrade")
	out.Header.Set("X-Forwarded-User", t.principal.UserID)
	if len(t.principal.Groups) > 0 {
		out.Header.Set("X-Forwarded-Groups", strings.Join(t.principal.Groups, ","))
	}
	stop := context.AfterFunc(request.Context(), func() {
		if out.Body != nil {
			_ = out.Body.Close()
		}
		lease.close()
	})
	if err := out.Write(stream); err != nil {
		stop()
		lease.close()
		if request.Context().Err() != nil {
			return nil, contextTunnelError(request.Context(), true)
		}
		return nil, tunnelError(CodeSourceDisconnected, true, err)
	}
	response, err := http.ReadResponse(bufio.NewReader(stream), out)
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
	if request.Header.Get("Upgrade") != "" || hasToken(request.Header.Values("Connection"), "upgrade") {
		return tunnelError(CodePermissionDenied, false, nil)
	}
	return nil
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

func stripIdentityHeaders(headers http.Header) {
	for name := range headers {
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		lower := strings.ToLower(canonical)
		switch {
		case lower == "authorization", lower == "proxy-authorization", lower == "cookie",
			lower == "forwarded", lower == "x-forwarded-user", lower == "x-forwarded-groups",
			strings.HasPrefix(lower, "x-forwarded-"), strings.HasPrefix(lower, "impersonate-"),
			lower == "proxy-connection", lower == "keep-alive":
			delete(headers, name)
		}
	}
}

func contextTunnelError(ctx context.Context, unknown bool) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return tunnelError(CodeSourceTimeout, unknown, ctx.Err())
	}
	return tunnelError(CodeSourceDisconnected, unknown, ctx.Err())
}

type streamLease struct {
	stream  *yamux.Stream
	release func()
	once    sync.Once
}

func (l *streamLease) close() {
	l.once.Do(func() {
		_ = l.stream.Close()
		_ = l.stream.SetDeadline(time.Now())
		l.release()
	})
}

type tunnelResponseBody struct {
	body  io.ReadCloser
	lease *streamLease
	stop  func() bool
	ctx   context.Context
	once  sync.Once
}

func (b *tunnelResponseBody) Read(data []byte) (int, error) {
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
	var err error
	b.once.Do(func() {
		b.stop()
		b.lease.close()
		err = b.body.Close()
	})
	return err
}

var _ http.RoundTripper = (*tunnelRoundTripper)(nil)
