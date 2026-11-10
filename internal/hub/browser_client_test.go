package hub

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/maomada/KubeAtlas/internal/cloud"
	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
)

func TestBrowserClientUsesScopedIdentityAndKeepsMCPClosed(t *testing.T) {
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	h, server := newTestHub(t)
	var observations []sourceObservation
	source := testAuthHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := pkgauth.UserFromContext(r.Context())
		observation := sourceObservation{
			Tunnel: cloud.IsAuthenticatedTunnelRequest(r.Context()), Auth: r.Header.Get("Authorization"),
			Cookie: r.Header.Get("Cookie"), Impersonate: r.Header.Get("Impersonate-User"),
			Host: r.Host,
		}
		if user != nil {
			observation.UserID = user.Username
			observation.Groups = append([]string(nil), user.Groups...)
		}
		observations = append(observations, observation)
		w.Header().Set("Set-Cookie", "source=must-not-escape")
		w.WriteHeader(http.StatusNoContent)
	}))
	startSource(t, cloudWebSocketEndpoint(server.URL), "alpha", "token-alpha", source)
	waitForAgent(t, h, "alpha")

	principal := Principal{UserID: "portal:user", Groups: []string{"radar:viewer"}, ClusterIDs: []string{"alpha"}}
	client, err := h.BrowserHTTPClient("alpha", principal)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, "http://localhost/api/resources?namespace=demo", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer browser-secret")
	request.Header.Set("Cookie", "session=browser-secret")
	request.Header.Set("Forwarded", "for=attacker")
	request.Header.Set("Impersonate-User", "attacker")
	request.Header.Set("X-Forwarded-User", "attacker")
	request.Header.Set("X-Radar-Role", "admin")
	request.Header.Set("Origin", "https://attacker.invalid")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.Header.Get("Set-Cookie") != "" {
		t.Fatalf("status=%d source cookie=%q", response.StatusCode, response.Header.Get("Set-Cookie"))
	}
	if len(observations) != 1 || observations[0].UserID != principal.UserID ||
		len(observations[0].Groups) != 1 || observations[0].Groups[0] != "radar:viewer" ||
		!observations[0].Tunnel || observations[0].Auth != "" || observations[0].Cookie != "" ||
		observations[0].Impersonate != "" || observations[0].Host != "localhost" {
		t.Fatalf("source received untrusted identity or headers: %+v", observations)
	}
	if _, err := h.BrowserHTTPClient("beta", principal); err == nil {
		t.Fatal("cross-cluster principal scope was accepted")
	}
	mcpClient, err := h.HTTPClient("alpha", principal)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mcpClient.Get("http://localhost/api/health")
	var tunnelErr *Error
	if !errors.As(err, &tunnelErr) || tunnelErr.Code != CodePermissionDenied {
		t.Fatalf("MCP client accepted browser API: %v", err)
	}
}

func TestBrowserRequestPolicyRejectsLocalRoutesAndUnsafeUpgrades(t *testing.T) {
	for _, path := range []string{
		"/api/contexts", "/api/config", "/api/desktop", "/api/local-terminal", "/api/fleet",
		"/api/cloud", "/api/connect", "/api/github", "/api/agent/self-upgrade",
		"/api/integrations/connections", "/api/traffic/connect", "/api/pods/ns/pod/files/save", "/api/resources%2fpods",
		"/api/resources/../contexts",
	} {
		request := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		if err := validateBrowserRequest(request, false); err == nil {
			t.Errorf("accepted forbidden path %q", path)
		}
	}
	readOnly := httptest.NewRequest(http.MethodPost, "http://localhost/api/resources/apply", nil)
	if err := validateBrowserRequest(readOnly, true); err == nil {
		t.Fatal("read-only principal was allowed to write resources")
	}
	preference := httptest.NewRequest(http.MethodPost, "http://localhost/api/cluster/namespace", nil)
	if err := validateBrowserRequest(preference, true); err != nil {
		t.Fatalf("read-only namespace preference denied: %v", err)
	}
	for _, path := range []string{"/api/traffic/source", "/api/traffic/connection"} {
		request := httptest.NewRequest(http.MethodPost, "http://localhost"+path, nil)
		if err := validateBrowserRequest(request, false); err == nil {
			t.Errorf("local traffic configuration write %q was allowed", path)
		}
	}
	evidence := httptest.NewRequest(http.MethodPost, "http://localhost/api/gitops/write-evidence", nil)
	if err := validateBrowserRequest(evidence, false); err != nil {
		t.Fatalf("GitOps write evidence route denied: %v", err)
	}
	if err := validateBrowserRequest(evidence, true); err == nil {
		t.Fatal("read-only principal was allowed to submit GitOps write evidence")
	}
	authMe := httptest.NewRequest(http.MethodGet, "http://localhost/api/auth/me?check=namespaces", nil)
	if err := validateBrowserRequest(authMe, false); err != nil {
		t.Fatalf("native auth/me namespace check denied: %v", err)
	}
	probe := httptest.NewRequest(http.MethodGet, "http://localhost/api/trace?probe=true", nil)
	if err := validateBrowserRequest(probe, true); err == nil {
		t.Fatal("read-only trace probe was allowed")
	}
	upgrade := httptest.NewRequest(http.MethodGet, "http://localhost/api/events/stream", nil)
	upgrade.Header.Set("Connection", "Upgrade")
	upgrade.Header.Set("Upgrade", "websocket")
	if err := validateBrowserRequest(upgrade, false); err == nil {
		t.Fatal("unapproved WebSocket upgrade was allowed")
	}
	exec := httptest.NewRequest(http.MethodGet, "http://localhost/api/pods/ns/pod/exec", nil)
	exec.Header.Set("Connection", "Upgrade")
	exec.Header.Set("Upgrade", "websocket")
	exec.Header.Set("Sec-WebSocket-Version", "13")
	exec.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	if err := validateBrowserRequest(exec, true); err == nil {
		t.Fatal("read-only WebSocket exec was allowed")
	}
}

func TestBrowserSSEFlushAndWebSocketDuplex(t *testing.T) {
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	h, server := newTestHub(t)
	upgrader := websocket.Upgrader{}
	source := testAuthHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/events/stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, "data: ready\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		messageType, message, err := conn.ReadMessage()
		if err == nil {
			_ = conn.WriteMessage(messageType, message)
		}
	}))
	startSource(t, cloudWebSocketEndpoint(server.URL), "alpha", "token-alpha", source)
	waitForAgent(t, h, "alpha")
	client, err := h.BrowserHTTPClient("alpha", Principal{UserID: "user", ClusterIDs: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sseRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/events/stream", nil)
	sse, err := client.Do(sseRequest)
	if err != nil {
		t.Fatal(err)
	}
	event := make([]byte, len("data: ready\n\n"))
	if _, err := io.ReadFull(sse.Body, event); err != nil || string(event) != "data: ready\n\n" {
		t.Fatalf("SSE did not flush promptly: data=%q err=%v", event, err)
	}
	cancel()
	_ = sse.Body.Close()

	wsRequest, _ := http.NewRequest(http.MethodGet, "http://localhost/api/pods/ns/pod/exec?command=sh", nil)
	wsRequest.Header.Set("Connection", "Upgrade")
	wsRequest.Header.Set("Upgrade", "websocket")
	wsRequest.Header.Set("Sec-WebSocket-Version", "13")
	wsRequest.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	wsResponse, err := client.Do(wsRequest)
	if err != nil {
		t.Fatal(err)
	}
	if wsResponse.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("WebSocket status=%d", wsResponse.StatusCode)
	}
	duplex, ok := wsResponse.Body.(io.ReadWriteCloser)
	if !ok {
		t.Fatalf("upgrade body is not duplex: %T", wsResponse.Body)
	}
	payload := []byte("hello")
	mask := []byte{1, 2, 3, 4}
	frame := []byte{0x81, 0x80 | byte(len(payload)), mask[0], mask[1], mask[2], mask[3]}
	for i, value := range payload {
		frame = append(frame, value^mask[i%4])
	}
	if _, err := duplex.Write(frame); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(duplex)
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		t.Fatal(err)
	}
	if header[0] != 0x81 || int(header[1]&0x7f) != len(payload) {
		t.Fatalf("unexpected echo frame header: %v", header)
	}
	echo := make([]byte, len(payload))
	if _, err := io.ReadFull(reader, echo); err != nil || string(echo) != string(payload) {
		t.Fatalf("WebSocket echo=%q err=%v", echo, err)
	}
	_ = duplex.Close()
}

func TestBrowserClientRetainsSourceResponseHeaderLimit(t *testing.T) {
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	h, server := newTestHub(t)
	source := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Large", strings.Repeat("x", maxResponseHeaderBytes))
		w.WriteHeader(http.StatusOK)
	})
	startSource(t, cloudWebSocketEndpoint(server.URL), "alpha", "token-alpha", source)
	waitForAgent(t, h, "alpha")
	client, err := h.BrowserHTTPClient("alpha", Principal{UserID: "user", ClusterIDs: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if maxResponseHeaderBytes != 64<<10 {
		t.Fatalf("source response header limit changed: %d", maxResponseHeaderBytes)
	}
	_, err = client.Get("http://localhost/api/health")
	if err == nil {
		t.Fatal("oversized source response headers were accepted")
	}
}
