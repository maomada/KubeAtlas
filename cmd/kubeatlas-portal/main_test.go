package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/hubstate"
	"github.com/maomada/KubeAtlas/internal/portal"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestVersionFlagDoesNotRequireCredentials(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "--listen", "127.0.0.1:0", "--version")
	cmd.Dir = "."
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "KUBEATLAS_PORTAL_USERNAME=") &&
			!strings.HasPrefix(entry, "KUBEATLAS_PORTAL_PASSWORD=") &&
			!strings.HasPrefix(entry, "KUBEATLAS_PORTAL_HUB_STATE_FILE=") &&
			!strings.HasPrefix(entry, "KUBEATLAS_PORTAL_HUB_GROUPS=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run --version failed: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "kubeatlas-portal dev" {
		t.Fatalf("unexpected --version output %q", got)
	}
}

func TestLoadCredentialsUsesLocalOwnerEnvironment(t *testing.T) {
	t.Setenv("KUBEATLAS_PORTAL_USERNAME", "local-owner")
	t.Setenv("KUBEATLAS_PORTAL_PASSWORD", "local-owner-password")
	credentials := loadCredentials()
	if credentials.Username != "local-owner" || credentials.Password != "local-owner-password" {
		t.Fatal("local owner credentials were not loaded")
	}
}

func TestListenPortOverride(t *testing.T) {
	address, err := resolveListenAddress("0.0.0.0:8080", 1333, false)
	if err != nil || address != "0.0.0.0:8080" {
		t.Fatalf("existing listen address: %q, %v", address, err)
	}
	address, err = resolveListenAddress("[::1]:1333", 1443, true)
	if err != nil || address != "[::1]:1443" {
		t.Fatalf("IPv6 listen override: %q, %v", address, err)
	}
	if _, err := resolveListenAddress("127.0.0.1:1333", 65536, true); err == nil {
		t.Fatal("accepted an invalid override port")
	}
}

func TestConfigureAdminReadsPasswordFileAndRequiresPair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte("new configuration password\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credentials := portal.Credentials{Username: "legacy", Password: "legacy password text"}
	if err := configureAdmin(&credentials, "", "", true); err == nil {
		t.Fatal("reset allowed without explicit credentials")
	}
	if err := configureAdmin(&credentials, "operator", "", false); err == nil {
		t.Fatal("accepted an incomplete administrator configuration")
	}
	if err := configureAdmin(&credentials, "operator", path, false); err != nil || credentials.Username != "operator" || credentials.Password != "new configuration password" {
		t.Fatalf("administrator password file was not loaded: %v", err)
	}
	if err := os.WriteFile(path, []byte("invalid UTF-8 password\xff"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := configureAdmin(&credentials, "operator", path, false); err == nil {
		t.Fatal("accepted a password file with invalid UTF-8")
	}
}

func TestConfigureAdminRejectsMultilinePasswordFile(t *testing.T) {
	for name, password := range map[string]string{
		"embedded LF":       "configuration\npassword",
		"embedded CR":       "configuration\rpassword",
		"embedded CRLF":     "configuration\r\npassword\r\n",
		"extra trailing LF": "configuration password\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "password")
			if err := os.WriteFile(path, []byte(password), 0o600); err != nil {
				t.Fatal(err)
			}
			credentials := portal.Credentials{Username: "legacy", Password: "legacy password text"}
			err := configureAdmin(&credentials, "operator", path, true)
			if err == nil || !strings.Contains(err.Error(), "single line") {
				t.Fatalf("expected a single-line password error, got %v", err)
			}
			if credentials.Username != "legacy" || credentials.Password != "legacy password text" {
				t.Fatal("invalid password file changed existing credentials")
			}
		})
	}
}

func TestConfigureHubRequiresPersistentState(t *testing.T) {
	credentials := portal.Credentials{}
	if err := configureHub(&credentials, "  ", "", ""); err == nil || !strings.Contains(err.Error(), "--hub-state-file is required") {
		t.Fatalf("missing Hub state error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "hub", "state.json")
	if err := configureHub(&credentials, path, "kubeatlas:operators", ""); err != nil {
		t.Fatal(err)
	}
	if credentials.Hub == nil || credentials.Hub.Store == nil || len(credentials.Hub.Groups) != 1 || credentials.Hub.Groups[0] != "kubeatlas:operators" {
		t.Fatal("Hub state and groups were not configured")
	}
}

func TestConfigureOIDCRequiresHubAndCompleteEnvironment(t *testing.T) {
	credentials := portal.Credentials{}
	if err := configureOIDC(&credentials, "", "", "", "", ""); err != nil || credentials.OIDC != nil {
		t.Fatalf("disabled OIDC configuration = %#v, err=%v", credentials.OIDC, err)
	}
	issuer := "https://idp.example/realms/kubeatlas"
	redirectURL := "https://portal.example/api/auth/oidc/callback"
	if err := configureOIDC(&credentials, issuer, "portal", redirectURL, "", "not-for-logs"); err == nil {
		t.Fatal("OIDC without Hub mode was accepted")
	}
	credentials.Hub = &portal.HubConfig{}
	if err := configureOIDC(&credentials, issuer, "portal", redirectURL, "", ""); err == nil {
		t.Fatal("OIDC with a missing client secret was accepted")
	}
	caCertFile := "/etc/kubeatlas/idp-ca.pem"
	if err := configureOIDC(&credentials, issuer, "portal", redirectURL, caCertFile, "not-for-logs"); err != nil {
		t.Fatal(err)
	}
	if credentials.OIDC == nil || credentials.OIDC.Issuer != issuer ||
		credentials.OIDC.ClientID != "portal" || credentials.OIDC.RedirectURL != redirectURL ||
		credentials.OIDC.CACertFile != caCertFile {
		t.Fatal("OIDC configuration fields were not retained")
	}
}

type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHubModeWiresLiveMCP(t *testing.T) {
	credentials := portal.Credentials{Username: "operator", Password: strings.Repeat("p", 32)}
	if err := configureHub(&credentials, filepath.Join(t.TempDir(), "hub", "state.json"), "kubeatlas:member", ""); err != nil {
		t.Fatal(err)
	}
	if credentials.Hub.MCPFactory == nil || len(credentials.Hub.Groups) != 1 || credentials.Hub.Groups[0] != "kubeatlas:member" {
		t.Fatal("live MCP factory or identity groups missing")
	}
	store := credentials.Hub.Store
	if _, err := store.CreateCluster(hubstate.ClusterInput{ID: "alpha", Name: "Alpha"}); err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf("portal:%x", sha256.Sum256([]byte(credentials.Username)))
	issued, err := store.Issue(owner, hubstate.IssueInput{Label: "fixture", ClusterIDs: []string{"alpha"}, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := portal.NewHandler(credentials, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handler.Close)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := &http.Client{Transport: testRoundTripper(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+issued.Token)
		return http.DefaultTransport.RoundTrip(r)
	})}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "fixture", Version: "1"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: client, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_clusters", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("live directory call failed")
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Source string `json:"source"`
		Data   struct {
			Clusters []struct {
				ClusterID string `json:"clusterId"`
			} `json:"clusters"`
		} `json:"data"`
	}
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	if response.Source != "directory" || len(response.Data.Clusters) != 1 || response.Data.Clusters[0].ClusterID != "alpha" {
		t.Fatal("MCP did not read the scoped Hub directory without a snapshot store")
	}
	if err := store.Revoke(owner, issued.Credential.ID); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/mcp", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	revoked, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer revoked.Body.Close()
	if revoked.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked PAT returned status %d", revoked.StatusCode)
	}
}

func TestCLIRejectsMissingHubState(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "--listen", "127.0.0.1:0")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "KUBEATLAS_PORTAL_HUB_STATE_FILE=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "--hub-state-file is required") {
		t.Fatalf("CLI output = %s, error = %v", output, err)
	}
}

func TestHTTPIPOriginFlagUsesEnvironmentAndExplicitOverride(t *testing.T) {
	fs := flag.NewFlagSet("portal", flag.ContinueOnError)
	value := registerHTTPIPOriginFlag(fs, func(key string) string {
		if key == "KUBEATLAS_PORTAL_HTTP_IP_ORIGIN" {
			return "http://192.0.2.10:9290"
		}
		return ""
	})
	if err := fs.Parse(nil); err != nil || *value != "http://192.0.2.10:9290" {
		t.Fatalf("environment default=%q, err=%v", *value, err)
	}
	fs = flag.NewFlagSet("portal", flag.ContinueOnError)
	value = registerHTTPIPOriginFlag(fs, func(string) string { return "http://192.0.2.10" })
	if err := fs.Parse([]string{"--http-ip-origin=http://[2001:db8::10]:9290"}); err != nil || *value != "http://[2001:db8::10]:9290" {
		t.Fatalf("explicit flag value=%q, err=%v", *value, err)
	}
}
