package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
		if !strings.HasPrefix(entry, "KUBEATLAS_PORTAL_TOKENS=") &&
			!strings.HasPrefix(entry, "KUBEATLAS_PORTAL_USERNAME=") &&
			!strings.HasPrefix(entry, "KUBEATLAS_PORTAL_PASSWORD=") {
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

func TestLoadCredentialsHubMode(t *testing.T) {
	t.Setenv("KUBEATLAS_PORTAL_TOKENS", "")
	if _, err := loadCredentials(false); err == nil {
		t.Fatal("legacy mode accepted a missing token map")
	}
	credentials, err := loadCredentials(true)
	if err != nil || credentials.Tokens == nil || len(credentials.Tokens) != 0 {
		t.Fatal("Hub-only mode should not require legacy registration tokens")
	}
	t.Setenv("KUBEATLAS_PORTAL_TOKENS", "{broken")
	if _, err := loadCredentials(true); err == nil {
		t.Fatal("Hub mode silently ignored a malformed explicit token map")
	}
}

type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHubModeWiresLiveMCP(t *testing.T) {
	credentials := portal.Credentials{Username: "operator", Password: strings.Repeat("p", 32)}
	if err := configureHub(&credentials, "", ""); err != nil || credentials.Hub != nil {
		t.Fatal("standalone configuration was changed")
	}
	if err := configureHub(&credentials, "", "platform-team"); err == nil {
		t.Fatal("Hub groups without Hub mode were silently ignored")
	}
	if err := configureHub(&credentials, filepath.Join(t.TempDir(), "hub", "state.json"), "radar:member"); err != nil {
		t.Fatal(err)
	}
	if credentials.Hub.MCPFactory == nil || len(credentials.Hub.Groups) != 1 || credentials.Hub.Groups[0] != "radar:member" {
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
	registry, err := portal.OpenRegistry(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := portal.NewHandler(registry, credentials, nil, nil)
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
		&mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp-fleet", HTTPClient: client, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
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
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/mcp-fleet", strings.NewReader(`{}`))
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
