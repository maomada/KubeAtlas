package hubstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConnectionSettingsPersistAndMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub", "state.json")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateConnections(ConnectionSettings{LANURL: "https://lan.example.test/"}); err != nil {
		t.Fatalf("valid test-domain origin was rejected: %v", err)
	}
	settings, err := store.UpdateConnections(ConnectionSettings{LANURL: "HTTPS://LAN.Example.:443/", PublicURL: "https://192.0.2.10:8443"})
	if err != nil {
		t.Fatal(err)
	}
	if settings.LANURL != "https://lan.example" || settings.PublicURL != "https://192.0.2.10:8443" {
		t.Fatalf("unexpected normalized settings: %+v", settings)
	}
	for _, invalid := range []ConnectionSettings{
		{LANURL: "https://user@example.test", PublicURL: ""},
		{LANURL: "https://example.test/path", PublicURL: ""},
		{LANURL: "https://*.example.test", PublicURL: ""},
		{LANURL: "http://192.0.2.10", PublicURL: ""},
	} {
		if _, err := store.UpdateConnections(invalid); err == nil {
			t.Errorf("accepted invalid origins: %+v", invalid)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]json.RawMessage
	if err := json.Unmarshal(data, &old); err != nil {
		t.Fatal(err)
	}
	delete(old, "connectionSettings")
	data, err = json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	migrated, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := migrated.Connections(); got != (ConnectionSettings{}) {
		t.Fatalf("legacy state did not backfill empty settings: %+v", got)
	}
}
