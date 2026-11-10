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

func TestConfigureOIDCRequiresHubAndCompleteEnvironment(t *testing.T) {
	credentials := portal.Credentials{}
	if err := configureOIDC(&credentials, "", "", "", "", ""); err != nil || credentials.OIDC != nil {
		t.Fatalf("disabled OIDC configuration = %#v, err=%v", credentials.OIDC, err)
	}
	if err := configureOIDC(&credentials, "https://idp.example/realms/kubeatlas", "portal", "https://portal.example/api/auth/oidc/callback", "", "not-for-logs"); err == nil {
		t.Fatal("OIDC without Hub mode was accepted")
	}
	credentials.Hub = &portal.HubConfig{}
	if err := configureOIDC(&credentials, "https://idp.example/realms/kubeatlas", "portal", "https://portal.example/api/auth/oidc/callback", "", ""); err == nil {
		t.Fatal("OIDC with a missing client secret was accepted")
	}
	if err := configureOIDC(&credentials, "https://idp.example/realms/kubeatlas", "portal", "https://portal.example/api/auth/oidc/callback", "/etc/kubeatlas/idp-ca.pem", "not-for-logs"); err != nil {
		t.Fatal(err)
	}
	if credentials.OIDC == nil || credentials.OIDC.Issuer != "https://idp.example/realms/kubeatlas" ||
		credentials.OIDC.ClientID != "portal" || credentials.OIDC.CACertFile != "/etc/kubeatlas/idp-ca.pem" {
		t.Fatal("OIDC configuration fields were not retained")
	}
}

func TestStatePaths(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(root, "registry.json")
	hubPath := filepath.Join(root, "hub.json")

	t.Run("plain duplicate", func(t *testing.T) {
		err := preflightStatePaths(registryPath, registryPath, "")
		if err == nil || !strings.Contains(err.Error(), "--data-file and --hub-state-file") {
			t.Fatalf("expected registry/Hub path collision, got %v", err)
		}
	})

	t.Run("relative duplicate", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		relativePath := filepath.Base(root) + "-relative-state.json"
		err = preflightStatePaths(relativePath, filepath.Join(cwd, relativePath), "")
		if err == nil || !strings.Contains(err.Error(), "state path collision") {
			t.Fatalf("expected relative/absolute path collision, got %v", err)
		}
	})

	t.Run("explicit Fleet collision", func(t *testing.T) {
		err := preflightStatePaths(registryPath, hubPath, hubPath)
		if err == nil || !strings.Contains(err.Error(), "--hub-state-file and --fleet-data-file") {
			t.Fatalf("expected Hub/Fleet path collision, got %v", err)
		}
	})

	t.Run("symlink parent with missing leaf", func(t *testing.T) {
		targetDir := filepath.Join(root, "target")
		if err := os.Mkdir(targetDir, 0o700); err != nil {
			t.Fatal(err)
		}
		linkDir := filepath.Join(root, "alias")
		if err := os.Symlink(targetDir, linkDir); err != nil {
			t.Fatal(err)
		}
		err := preflightStatePaths(filepath.Join(targetDir, "state.json"), filepath.Join(linkDir, "state.json"), "")
		if err == nil || !strings.Contains(err.Error(), "state path collision") {
			t.Fatalf("expected missing-leaf symlink alias collision, got %v", err)
		}
	})

	t.Run("existing hardlink alias", func(t *testing.T) {
		first := filepath.Join(root, "hardlink-a.json")
		second := filepath.Join(root, "hardlink-b.json")
		if err := os.WriteFile(first, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(first, second); err != nil {
			t.Skipf("hard links are unavailable: %v", err)
		}
		err := preflightStatePaths(first, second, "")
		if err == nil || !strings.Contains(err.Error(), "state path collision") {
			t.Fatalf("expected SameFile alias collision, got %v", err)
		}
	})

	t.Run("symlink and parent traversal", func(t *testing.T) {
		targetDir := filepath.Join(root, "target", "nested")
		if err := os.MkdirAll(targetDir, 0o700); err != nil {
			t.Fatal(err)
		}
		linkDir := filepath.Join(root, "nested-alias")
		if err := os.Symlink(targetDir, linkDir); err != nil {
			t.Fatal(err)
		}
		throughLink := linkDir + string(filepath.Separator) + ".." + string(filepath.Separator) + "state.json"
		direct := filepath.Join(root, "target", "state.json")
		err := preflightStatePaths(throughLink, direct, "")
		if err == nil || !strings.Contains(err.Error(), "parent traversal is not permitted") {
			t.Fatalf("expected ambiguous symlink/.. path rejection, got %v", err)
		}
	})

	t.Run("rejects parent traversal after a missing component", func(t *testing.T) {
		ambiguous := filepath.Join(root, "missing") + string(filepath.Separator) + ".." + string(filepath.Separator) + "state.json"
		err := preflightStatePaths(ambiguous, filepath.Join(root, "state.json"), "")
		if err == nil || !strings.Contains(err.Error(), "parent traversal is not permitted") {
			t.Fatalf("expected missing-path traversal rejection, got %v", err)
		}
	})

	t.Run("distinct paths and effective Fleet modes", func(t *testing.T) {
		defaultFleet := filepath.Join(root, "fleet", "state.json")
		if got := effectiveFleetStatePath(registryPath, "", ""); got != defaultFleet {
			t.Fatalf("standalone default Fleet path = %q, want %q", got, defaultFleet)
		}
		if got := effectiveFleetStatePath(registryPath, hubPath, ""); got != "" {
			t.Fatalf("Hub-only Fleet path = %q, want none", got)
		}
		explicitFleet := filepath.Join(root, "explicit-fleet.json")
		if got := effectiveFleetStatePath(registryPath, hubPath, explicitFleet); got != explicitFleet {
			t.Fatalf("explicit Hub Fleet path = %q, want %q", got, explicitFleet)
		}
		if err := preflightStatePaths(registryPath, hubPath, explicitFleet); err != nil {
			t.Fatalf("distinct active paths rejected: %v", err)
		}
		if err := preflightStatePaths(registryPath, "", defaultFleet); err != nil {
			t.Fatalf("standalone default Fleet path rejected: %v", err)
		}
		if err := preflightStatePaths(registryPath, hubPath, ""); err != nil {
			t.Fatalf("Hub-only paths rejected: %v", err)
		}
	})

	t.Run("CLI rejects collision before parsing stores", func(t *testing.T) {
		sentinelPath := filepath.Join(root, "sentinel.json")
		sentinel := []byte("preserve this invalid store")
		if err := os.WriteFile(sentinelPath, sentinel, 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("go", "run", ".", "--data-file", sentinelPath, "--hub-state-file", sentinelPath)
		cmd.Dir = "."
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "KUBEATLAS_PORTAL_TOKENS=") &&
				!strings.HasPrefix(entry, "KUBEATLAS_PORTAL_HUB_STATE_FILE=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("CLI accepted colliding state paths: %s", output)
		}
		if !strings.Contains(string(output), "state path collision: --data-file and --hub-state-file") {
			t.Fatalf("CLI did not reject paths before store parsing: %s", output)
		}
		if strings.Contains(string(output), "decode portal registry") {
			t.Fatalf("CLI parsed the sentinel registry before rejecting paths: %s", output)
		}
		got, err := os.ReadFile(sentinelPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(sentinel) {
			t.Fatalf("CLI changed sentinel file: got %q, want %q", got, sentinel)
		}
	})
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
