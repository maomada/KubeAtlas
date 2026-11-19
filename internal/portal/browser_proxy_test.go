package portal

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/auth"
	"github.com/maomada/KubeAtlas/internal/cloud"
	"github.com/maomada/KubeAtlas/internal/hub"
	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
)

func TestBrowserRequestRewriteOwnsURLAndPreservesQuery(t *testing.T) {
	for _, query := range []string{"namespace=demo&label=a%2Fb", "invalid=%zz&empty", ""} {
		t.Run(query, func(t *testing.T) {
			incoming := httptest.NewRequest(http.MethodGet, "http://portal.example/c/alpha/api/resources/pods", nil)
			incoming.URL.RawQuery = query
			incoming.URL.ForceQuery = query == ""
			original := *incoming.URL
			outgoing := incoming.Clone(incoming.Context())
			// Exercise the alias directly, even though ReverseProxy normally clones it.
			outgoing.URL = incoming.URL
			rewriteBrowserRequest(&httputil.ProxyRequest{In: incoming, Out: outgoing}, "/api/resources/pods")
			if outgoing.URL == incoming.URL || *incoming.URL != original {
				t.Fatal("rewrite changed the inbound URL or retained its alias")
			}
			if outgoing.URL.Scheme != "http" || outgoing.URL.Host != "localhost" ||
				outgoing.Host != "localhost" || outgoing.URL.Path != "/api/resources/pods" ||
				outgoing.URL.RawPath != "" || outgoing.RequestURI != "" ||
				outgoing.URL.RawQuery != query || outgoing.URL.ForceQuery != incoming.URL.ForceQuery {
				t.Fatalf("unexpected outbound request: host=%q uri=%q url=%+v", outgoing.Host, outgoing.RequestURI, outgoing.URL)
			}
		})
	}
}

func TestBrowserProxyConnectionHeadersCannotStripTunnelIdentity(t *testing.T) {
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	transport, err := hub.New(hub.Config{AuthenticateAgent: func(_ context.Context, token, clusterID string) (hub.AgentIdentity, error) {
		if token != "test-agent-token" || clusterID != "alpha" {
			return hub.AgentIdentity{}, errors.New("unknown test agent")
		}
		return hub.AgentIdentity{ClusterID: "alpha", Name: "Alpha"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	server := httptest.NewServer(transport)
	t.Cleanup(server.Close)
	type observation struct {
		user, host, path, query, auth, cookie string
		groups                                []string
		tunnel                                bool
	}
	observed := make(chan observation, 1)
	source := auth.Authenticate(auth.Config{Mode: "proxy", Secret: "test-proxy-secret"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := observation{
			host: r.Host, path: r.URL.Path, query: r.URL.RawQuery,
			auth: r.Header.Get("Authorization"), cookie: r.Header.Get("Cookie"),
			tunnel: cloud.IsAuthenticatedTunnelRequest(r.Context()),
		}
		if user := pkgauth.UserFromContext(r.Context()); user != nil {
			got.user, got.groups = user.Username, append([]string(nil), user.Groups...)
		}
		observed <- got
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- cloud.Run(ctx, cloud.Config{
			URL:   "ws" + strings.TrimPrefix(server.URL, "http") + "/agent",
			Token: "test-agent-token", ClusterID: "alpha", ClusterName: "Alpha", Handler: source,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("test Agent did not stop")
		}
	})
	ready, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for len(transport.Agents()) == 0 {
		select {
		case <-tick.C:
		case <-ready.Done():
			t.Fatal("test Agent did not become ready")
		}
	}
	principal := hub.Principal{UserID: "portal:user", Groups: []string{"kubeatlas:viewer"}, ClusterIDs: []string{"alpha"}}
	client, err := transport.BrowserHTTPClient("alpha", principal)
	if err != nil {
		t.Fatal(err)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			rewriteBrowserRequest(request, "/api/resources/pods")
		},
		Transport: client.Transport,
	}
	request := httptest.NewRequest(http.MethodGet, "http://portal.example/c/alpha/api/resources/pods?namespace=demo&label=a%2Fb", nil)
	request.Header.Set("Connection", "X-Forwarded-User, X-Forwarded-Groups, Host")
	request.Header.Set("X-Forwarded-User", "forged-user")
	request.Header.Set("X-Forwarded-Groups", "system:masters")
	request.Header.Set("Authorization", "Bearer browser-secret")
	request.Header.Set("Cookie", "session=browser-secret")
	stripPortalProxyHeaders(request.Header)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("proxy status=%d body=%q", response.Code, response.Body.String())
	}
	select {
	case got := <-observed:
		if got.user != principal.UserID || len(got.groups) != 1 || got.groups[0] != principal.Groups[0] ||
			got.host != "localhost" || got.path != "/api/resources/pods" ||
			got.query != "namespace=demo&label=a%2Fb" || got.auth != "" || got.cookie != "" || !got.tunnel {
			t.Fatalf("source received altered routing, identity or browser credentials: %+v", got)
		}
	default:
		t.Fatal("request did not reach the selected source")
	}
	if request.URL.Host != "portal.example" || request.URL.Path != "/c/alpha/api/resources/pods" {
		t.Fatalf("proxy changed the original target: %+v", request.URL)
	}
}

func TestBrowserProxyRewriteKeepsRawQuery(t *testing.T) {
	const query = "namespace=demo&invalid=%zz&label=a%2Fb"
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			rewriteBrowserRequest(request, "/api/resources/pods")
		},
		Transport: browserProxyRoundTripper(func(request *http.Request) (*http.Response, error) {
			if request.URL.RawQuery != query {
				t.Errorf("query bytes changed: got %q want %q", request.URL.RawQuery, query)
			}
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		}),
	}
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://portal.example/c/alpha/api/resources/pods?"+query, nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("proxy status=%d body=%q", response.Code, response.Body.String())
	}
}

type browserProxyRoundTripper func(*http.Request) (*http.Response, error)

func (f browserProxyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
