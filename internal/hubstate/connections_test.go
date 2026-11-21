package hubstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitialHubOriginOnlyAppliesToNewState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub", "state.json")
	for _, invalid := range []string{"http://10.2.0.2", "https://hub.example.test/subpath"} {
		if _, err := OpenStoreWithInitialOrigin(path, invalid); err == nil {
			t.Errorf("accepted invalid initial origin %q", invalid)
		}
	}
	store, err := OpenStoreWithInitialOrigin(path, "https://Hub.Example.Test:443/")
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Connections().PublicURL; got != "https://hub.example.test" {
		t.Fatalf("initial public origin = %q", got)
	}

	for _, settings := range []ConnectionSettings{
		{LANURL: "https://internal.example.test", PublicURL: "https://saved.example.test"},
		{},
	} {
		if _, err := store.UpdateConnections(settings); err != nil {
			t.Fatal(err)
		}
		reopened, err := OpenStoreWithInitialOrigin(path, "https://other.example.test")
		if err != nil {
			t.Fatal(err)
		}
		if got := reopened.Connections(); got != settings {
			t.Fatalf("changed stored connections: got %+v, want %+v", got, settings)
		}
		store = reopened
	}
}

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
		{LANURL: "http://192.0.2.10", PublicURL: "https://192.0.2.10"},
		{LANURL: "https://user@example.test", PublicURL: ""},
		{LANURL: "https://example.test/path", PublicURL: ""},
		{LANURL: "https://*.example.test", PublicURL: ""},
		{LANURL: "http://hub.example.test", PublicURL: ""},
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
	old["version"] = json.RawMessage("1")
	delete(old, "organizations")
	delete(old, "accounts")
	delete(old, "memberships")
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
