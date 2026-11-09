package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validHubStartupFlags(t *testing.T) hubStartupFlags {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hub-token")
	if err := os.WriteFile(path, []byte(strings.Repeat("t", 32)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return hubStartupFlags{
		URL:            "https://hub.example.test",
		ClusterID:      "prod-east-1",
		TokenFile:      path,
		AuthMode:       "none",
		CredentialMode: "impersonate",
		MCPEnabled:     true,
	}
}

func TestResolveHubStartupDerivesSecureAndLoopbackEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name     string
		origin   string
		endpoint string
	}{
		{"secure", "https://hub.example.test/", "wss://hub.example.test/agent"},
		{"loopback", "http://127.0.0.1:8080", "ws://127.0.0.1:8080/agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := validHubStartupFlags(t)
			flags.URL = tc.origin
			got, err := resolveHubStartup(flags)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Enabled || got.Origin != strings.TrimSuffix(tc.origin, "/") || got.AgentURL != tc.endpoint {
				t.Fatalf("resolved Hub startup = %+v", got)
			}
			if got.AuthMode != "proxy" || got.UserHeader != "X-Forwarded-User" || got.GroupsHeader != "X-Forwarded-Groups" {
				t.Fatalf("proxy identity was not pinned: %+v", got)
			}
			if got.ClusterID != flags.ClusterID || got.Token != strings.Repeat("t", 32) {
				t.Fatalf("resolved identity/token mismatch")
			}
		})
	}
}

func TestResolveHubStartupLoadsPrivateCA(t *testing.T) {
	flags := validHubStartupFlags(t)
	flags.CAFile = writeHubTestCA(t)
	got, err := resolveHubStartup(flags)
	if err != nil {
		t.Fatal(err)
	}
	if got.CA == nil || len(got.CA.Subjects()) == 0 {
		t.Fatal("private CA was not loaded")
	}
}

func TestResolveHubStartupAllowsProjectedSecretSymlink(t *testing.T) {
	flags := validHubStartupFlags(t)
	projected := filepath.Join(t.TempDir(), "projected-token")
	if err := os.WriteFile(projected, []byte(strings.Repeat("p", 32)), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "token")
	if err := os.Symlink(projected, link); err != nil {
		t.Fatal(err)
	}
	flags.TokenFile = link
	got, err := resolveHubStartup(flags)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != strings.Repeat("p", 32) {
		t.Fatal("token was not read through projected Secret symlink")
	}
}

func TestResolveHubStartupRejectsPartialConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*hubStartupFlags)
	}{
		{"url only", func(f *hubStartupFlags) { f.ClusterID, f.TokenFile = "", "" }},
		{"cluster only", func(f *hubStartupFlags) { f.URL, f.TokenFile = "", "" }},
		{"token file only", func(f *hubStartupFlags) { f.URL, f.ClusterID = "", "" }},
		{"CA only", func(f *hubStartupFlags) { f.URL, f.ClusterID, f.TokenFile, f.CAFile = "", "", "", "/ca.crt" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := validHubStartupFlags(t)
			tc.edit(&flags)
			if _, err := resolveHubStartup(flags); err == nil {
				t.Fatal("partial Hub configuration was accepted")
			}
		})
	}
}

func TestResolveHubStartupRejectsInvalidOriginsAndIDs(t *testing.T) {
	for _, raw := range []string{
		"https://user:password@hub.example.test",
		"https://hub.example.test/portal",
		"https://hub.example.test?tenant=one",
		"https://hub.example.test#fragment",
		"http://hub.example.test",
	} {
		t.Run(raw, func(t *testing.T) {
			flags := validHubStartupFlags(t)
			flags.URL = raw
			if _, err := resolveHubStartup(flags); err == nil {
				t.Fatalf("invalid Hub origin %q was accepted", raw)
			}
		})
	}

	for _, id := range []string{"", "-prod", "Prod", "has_underscore", strings.Repeat("a", 64)} {
		t.Run("cluster-id_"+id, func(t *testing.T) {
			flags := validHubStartupFlags(t)
			flags.ClusterID = id
			if _, err := resolveHubStartup(flags); err == nil {
				t.Fatalf("invalid cluster ID %q was accepted", id)
			}
		})
	}
}

func TestResolveHubStartupRejectsUnreadableCredentialFiles(t *testing.T) {
	flags := validHubStartupFlags(t)
	flags.TokenFile = filepath.Join(t.TempDir(), "missing-token")
	if _, err := resolveHubStartup(flags); err == nil {
		t.Fatal("missing token file was accepted")
	}

	flags = validHubStartupFlags(t)
	flags.CAFile = filepath.Join(t.TempDir(), "missing-ca")
	if _, err := resolveHubStartup(flags); err == nil {
		t.Fatal("missing CA file was accepted")
	}
}

func TestResolveHubStartupRejectsConflictingModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*hubStartupFlags)
	}{
		{"legacy Cloud", func(f *hubStartupFlags) { f.LegacyCloudConfigured = true }},
		{"OIDC", func(f *hubStartupFlags) { f.AuthMode = "oidc" }},
		{"user token", func(f *hubStartupFlags) { f.CredentialMode = "user-token" }},
		{"base path", func(f *hubStartupFlags) { f.BasePath = "/prefix" }},
		{"MCP disabled", func(f *hubStartupFlags) { f.MCPEnabled = false }},
		{"catalog only", func(f *hubStartupFlags) { f.MCPCatalogOnly = true }},
		{"catalog stdio", func(f *hubStartupFlags) { f.MCPCatalogStdio = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := validHubStartupFlags(t)
			tc.edit(&flags)
			if _, err := resolveHubStartup(flags); err == nil {
				t.Fatal("incompatible configuration was accepted")
			}
		})
	}
}

func TestResolveHubStartupStandaloneDoesNotChangeDefaults(t *testing.T) {
	got, err := resolveHubStartup(hubStartupFlags{AuthMode: "oidc", BasePath: "/portal"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.AuthMode != "" || got.UserHeader != "" || got.GroupsHeader != "" {
		t.Fatalf("standalone startup was changed: %+v", got)
	}
}

func TestReadHubTokenFileValidationAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{"short", "short"},
		{"embedded space", strings.Repeat("x", 16) + " " + strings.Repeat("y", 16)},
		{"embedded newline", strings.Repeat("x", 16) + "\n" + strings.Repeat("y", 16)},
		{"too long", strings.Repeat("z", maxHubTokenBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := readHubTokenFile(path)
			if err == nil {
				t.Fatal("invalid token file was accepted")
			}
			if strings.Contains(err.Error(), tc.data) {
				t.Fatal("token contents appeared in an error")
			}
		})
	}
}

func TestReadHubTokenFileTrimsNewlineWithinLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	token := strings.Repeat("m", maxHubTokenBytes-1)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readHubTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != token {
		t.Fatal("maximum-length token was not preserved")
	}
}

func writeHubTestCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "hub test CA"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "hub-ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
