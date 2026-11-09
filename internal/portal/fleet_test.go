package portal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/maomada/KubeAtlas/internal/fleetsync"
)

func TestPortalFleetMCPProxyHostProtection(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "default"
		if configured {
			name = "explicit-proxy-hosts"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			registry, err := OpenRegistry(filepath.Join(dir, "registry.json"))
			if err != nil {
				t.Fatal(err)
			}
			store, err := fleetsync.OpenStore(filepath.Join(dir, "fleet", "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			credentials := Credentials{Username: testUsername, Password: testPassword}
			if configured {
				credentials.MCPAllowedHosts = []string{"10.0.101.165:9445", "portal.example:443", "[2001:db8::1]:9445"}
			}
			handler, err := NewHandler(registry, credentials, nil, store)
			if err != nil {
				t.Fatal(err)
			}
			defer handler.Close()
			server := httptest.NewServer(handler)
			defer server.Close()
			cookie := loginForTest(t, handler)
			externalStatus := http.StatusForbidden
			if configured {
				externalStatus = http.StatusOK
			}
			for _, test := range []struct {
				name, host, origin, site, forwarded string
				noSession                           bool
				want                                int
			}{
				{name: "loopback", host: "localhost:9290", want: http.StatusOK},
				{name: "external IP", host: "10.0.101.165:9445", want: externalStatus},
				{name: "external DNS", host: "portal.example:443", origin: "https://portal.example:443", want: externalStatus},
				{name: "external IPv6", host: "[2001:db8::1]:9445", want: externalStatus},
				{name: "unknown", host: "attacker.example", want: http.StatusForbidden},
				{name: "wrong port", host: "10.0.101.165:9446", want: http.StatusForbidden},
				{name: "suffix", host: "portal.example.attacker:443", want: http.StatusForbidden},
				{name: "forwarded host is not trusted", host: "attacker.example", forwarded: "10.0.101.165:9445", want: http.StatusForbidden},
				{name: "cross origin", host: "10.0.101.165:9445", origin: "https://attacker.example", want: http.StatusForbidden},
				{name: "cross site", host: "10.0.101.165:9445", site: "cross-site", want: http.StatusForbidden},
				{name: "no session", host: "10.0.101.165:9445", noSession: true, want: http.StatusUnauthorized},
			} {
				t.Run(test.name, func(t *testing.T) {
					for _, method := range []string{"initialize", "tools/list"} {
						body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
						if method == "initialize" {
							body = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"proxy-test","version":"1"}}}`
						}
						req, err := http.NewRequest(http.MethodPost, server.URL+"/mcp-fleet", strings.NewReader(body))
						if err != nil {
							t.Fatal(err)
						}
						req.Host = test.host
						req.Header.Set("Content-Type", "application/json")
						req.Header.Set("Accept", "application/json, text/event-stream")
						req.Header.Set("Origin", test.origin)
						req.Header.Set("Sec-Fetch-Site", test.site)
						req.Header.Set("X-Forwarded-Host", test.forwarded)
						if !test.noSession {
							req.AddCookie(cookie)
						}
						response, err := server.Client().Do(req)
						if err != nil {
							t.Fatal(err)
						}
						defer response.Body.Close()
						if response.StatusCode != test.want {
							t.Fatalf("%s status=%d want=%d", method, response.StatusCode, test.want)
						}
						if test.want == http.StatusOK {
							var rpc struct {
								Result json.RawMessage
								Error  json.RawMessage
							}
							if err := json.NewDecoder(response.Body).Decode(&rpc); err != nil || len(rpc.Result) == 0 || len(rpc.Error) != 0 {
								t.Fatalf("%s RPC response=%+v err=%v", method, rpc, err)
							}
						}
					}
				})
			}
		})
	}
}

func TestPortalFleetScopeAndPersistence(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "fleet", "state.json")
	store, err := fleetsync.OpenStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := OpenRegistry(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{
		"cluster-a": testToken,
		"cluster-b": "other-token-with-at-least-thirty-two-characters",
	}
	handler, err := NewHandler(registry, Credentials{
		Tokens: tokens, Username: testUsername, Password: testPassword, AllowInsecureCookies: true,
	}, fstest.MapFS{"portal.html": &fstest.MapFile{Data: []byte("portal-shell")}}, store)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	defer handler.Close()

	snapshot := fleetsync.Snapshot{
		Version: fleetsync.Version, ClusterID: "cluster-a", Name: "A",
		SourceID: fleetsync.SourceIDForBinding("source-a"), ObservedAt: time.Now().UTC(),
		Documents: []fleetsync.Document{}, Limitations: []string{},
	}
	request := func(method, path string, body any, token string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		var payload bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&payload).Encode(body); err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, path, &payload)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	registration := Registration{
		ID: "cluster-b", Name: "Cluster B", RuntimeMode: "local",
		RadarURL: "https://cluster-b.example", RadarVersion: "test",
		ConnectionState: "connected", AuthMode: "none",
		MCP: MCPRegistration{Mode: "disabled", Transport: "streamable-http"},
	}
	if got := request(http.MethodPut, "/api/registrations/cluster-b", registration, tokens["cluster-b"], nil); got.Code != http.StatusNoContent {
		t.Fatalf("existing Portal registration status = %d body=%s", got.Code, got.Body)
	}
	if got := request(http.MethodPost, "/api/fleet/snapshots/cluster-a", snapshot, tokens["cluster-b"], nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong cluster token status = %d", got.Code)
	}
	mismatch := snapshot
	mismatch.ClusterID = "cluster-b"
	if got := request(http.MethodPost, "/api/fleet/snapshots/cluster-a", mismatch, tokens["cluster-a"], nil); got.Code != http.StatusBadRequest {
		t.Fatalf("mismatched snapshot identity status = %d", got.Code)
	}
	if got := request(http.MethodPost, "/api/fleet/snapshots/cluster-a", snapshot, tokens["cluster-a"], nil); got.Code != http.StatusOK {
		t.Fatalf("valid snapshot upload status = %d body=%s", got.Code, got.Body)
	}

	cookie := loginForTest(t, handler)
	directoryResponse := request(http.MethodGet, "/api/clusters", nil, "", cookie)
	var directory ClusterList
	if directoryResponse.Code != http.StatusOK || json.Unmarshal(directoryResponse.Body.Bytes(), &directory) != nil ||
		len(directory.Clusters) != 1 || directory.Clusters[0].ID != "cluster-b" {
		t.Fatalf("existing Portal directory response = %d %s", directoryResponse.Code, directoryResponse.Body)
	}
	inventoryResponse := request(http.MethodGet, "/api/fleet/clusters", nil, "", cookie)
	var inventory []fleetsync.ClusterContext
	if inventoryResponse.Code != http.StatusOK || json.Unmarshal(inventoryResponse.Body.Bytes(), &inventory) != nil ||
		len(inventory) != 1 || inventory[0].ClusterID != "cluster-a" {
		t.Fatalf("viewer inventory response = %d %s", inventoryResponse.Code, inventoryResponse.Body)
	}
	group := fleetsync.Group{ID: "production", Name: "Production", ClusterIDs: []string{"cluster-a", "cluster-b"}}
	if got := request(http.MethodPut, "/api/fleet/fleets/production", group, "", cookie); got.Code != http.StatusOK {
		t.Fatalf("viewer group update status = %d body=%s", got.Code, got.Body)
	}
	contextResponse := request(http.MethodPost, "/api/fleet/context", fleetsync.Selection{FleetID: group.ID}, "", cookie)
	var fleetContext fleetsync.AgentContext
	if contextResponse.Code != http.StatusOK || json.Unmarshal(contextResponse.Body.Bytes(), &fleetContext) != nil ||
		len(fleetContext.Clusters) != 2 {
		t.Fatalf("viewer group context response = %d %s", contextResponse.Code, contextResponse.Body)
	}
	listed := request(http.MethodPost, "/api/fleet/tools", fleetsync.Selection{FleetID: group.ID}, "", cookie)
	if listed.Code != http.StatusOK {
		t.Fatalf("tool catalog status = %d body=%s", listed.Code, listed.Body)
	}
	var catalogs []fleetsync.ClusterTools
	if err := json.Unmarshal(listed.Body.Bytes(), &catalogs); err != nil {
		t.Fatal(err)
	}
	if len(catalogs) != 2 || catalogs[0].ClusterID != "cluster-a" || catalogs[0].State != "unavailable" ||
		len(catalogs[0].Tools) != 0 || catalogs[1].ClusterID != "cluster-b" ||
		catalogs[1].State != "unavailable" || len(catalogs[1].Tools) != 0 {
		t.Fatalf("group tool catalog omitted unavailable members: %+v", catalogs)
	}
	if got := request(http.MethodGet, "/api/fleet/snapshots", nil, "", cookie); got.Code != http.StatusNotFound {
		t.Fatalf("viewer route exposed generic snapshots endpoint: %d", got.Code)
	}
	if got := request(http.MethodGet, "/api/fleet/clusters/extra", nil, "", cookie); got.Code != http.StatusNotFound {
		t.Fatalf("unallowlisted Fleet route leaked: %d", got.Code)
	}

	handler.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := fleetsync.OpenStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	context, err := reopened.Context(fleetsync.Selection{FleetID: group.ID})
	if err != nil || len(context.Clusters) != 2 || context.Clusters[1].State != "unavailable" {
		t.Fatalf("persisted group context = %+v, err = %v", context, err)
	}
}

func TestPortalFleetRoutesReturnUnavailableWithoutStore(t *testing.T) {
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(registry, Credentials{
		Username: testUsername, Password: testPassword, AllowInsecureCookies: true,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	cookie := loginForTest(t, handler)
	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/fleet/clusters"},
		{http.MethodPost, "/mcp-fleet"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s status = %d, want %d", route.method, route.path, response.Code, http.StatusServiceUnavailable)
		}
	}
}
