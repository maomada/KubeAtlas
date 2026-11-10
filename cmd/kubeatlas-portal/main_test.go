package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/internal/portal"
)

func TestLoadCredentialsUsesLocalOwnerEnvironment(t *testing.T) {
	t.Setenv("KUBEATLAS_PORTAL_USERNAME", "local-owner")
	t.Setenv("KUBEATLAS_PORTAL_PASSWORD", "local-owner-password")
	credentials := loadCredentials()
	if credentials.Username != "local-owner" || credentials.Password != "local-owner-password" {
		t.Fatal("local owner credentials were not loaded")
	}
}

func TestConfigureHubRequiresPersistentState(t *testing.T) {
	credentials := portal.Credentials{}
	if err := configureHub(&credentials, "  ", ""); err == nil || !strings.Contains(err.Error(), "--hub-state-file is required") {
		t.Fatalf("missing Hub state error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "hub", "state.json")
	if err := configureHub(&credentials, path, "radar:operators"); err != nil {
		t.Fatal(err)
	}
	if credentials.Hub == nil || credentials.Hub.Store == nil || len(credentials.Hub.Groups) != 1 || credentials.Hub.Groups[0] != "radar:operators" {
		t.Fatal("Hub state and groups were not configured")
	}
}

func TestConfigureOIDCRequiresHubState(t *testing.T) {
	if err := configureOIDC(&portal.Credentials{}, "https://issuer.example", "client", "https://portal.example/callback", "", "secret"); err == nil {
		t.Fatal("OIDC accepted missing Hub state")
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
