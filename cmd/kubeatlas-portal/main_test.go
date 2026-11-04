package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
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
