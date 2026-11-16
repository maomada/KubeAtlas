package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetiredFleetPackagesRemainAbsent(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"internal/fleet",
		"internal/fleetsync",
		"internal/portalagent",
		"cmd/explorer/fleet.go",
		"internal/app/fleet_sync.go",
		"internal/app/portal.go",
		"internal/mcp/collection_plan.go",
		"internal/portal/registry.go",
		"internal/portal/agent_hub.go",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Errorf("retired path %s still exists or could not be checked: %v", path, err)
		}
	}
	legacyMCPFiles, err := filepath.Glob(filepath.Join(root, "internal", "mcp", "fleet*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyMCPFiles) != 0 {
		t.Errorf("retired Fleet MCP files remain: %v", legacyMCPFiles)
	}

	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || filepath.Clean(path) == filepath.Join(root, "cmd/explorer/legacy_removal_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, importPath := range []string{
			"/internal/fleet",
			"/internal/fleetsync",
			"/internal/portalagent",
		} {
			if strings.Contains(string(source), importPath) {
				t.Errorf("%s references retired package %s", relative, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
