package portal

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/internal/hubartifacts"
)

func TestEnrollmentBootstrapCommandPinsDownloadAndRejectsRedirects(t *testing.T) {
	work := t.TempDir()
	bin := filepath.Join(work, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(work, "bootstrap.fixture")
	marker := filepath.Join(work, "executed")
	body := []byte("printf executed > " + shellQuote(marker) + "\n")
	if err := os.WriteFile(bodyPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	curlText := strings.Join([]string{
		"#!/bin/bash",
		"set -e",
		"output=''",
		"while (($#)); do",
		"  if [[ $1 == --output ]]; then output=$2; shift 2; else shift; fi",
		"done",
		"cat \"$MOCK_BOOTSTRAP_BODY\" > \"$output\"",
		"printf '%s' \"$MOCK_HTTP_STATUS\"",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(curlText), 0o700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	availability := hubartifacts.Availability{Version: "1.2.3", BootstrapSHA256: hex.EncodeToString(digest[:])}
	command := buildEnrollmentBootstrapCommand("lan", "https://hub.example", availability)
	if !strings.Contains(command, "set -Eeuo pipefail") || !strings.Contains(command, "umask 077") || strings.Contains(command, "--location") {
		t.Fatalf("bootstrap command is not fail-closed: %s", command)
	}
	pathValue := bin + string(os.PathListSeparator) + os.Getenv("PATH")
	run := func(status string, pinned hubartifacts.Availability) ([]byte, error) {
		t.Helper()
		_ = os.Remove(marker)
		cmd := exec.Command("/bin/bash", "-c", buildEnrollmentBootstrapCommand("lan", "https://hub.example", pinned))
		cmd.Env = append(os.Environ(), "PATH="+pathValue, "MOCK_BOOTSTRAP_BODY="+bodyPath, "MOCK_HTTP_STATUS="+status)
		return cmd.CombinedOutput()
	}
	if output, err := run("200", availability); err != nil {
		t.Fatalf("valid pinned bootstrap failed: %v: %s", err, output)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "executed" {
		t.Fatalf("verified bootstrap did not execute: data=%q err=%v", data, err)
	}
	for _, test := range []struct {
		name         string
		status       string
		availability hubartifacts.Availability
	}{
		{name: "redirect with executable body", status: "302", availability: availability},
		{name: "checksum mismatch", status: "200", availability: hubartifacts.Availability{Version: "1.2.3", BootstrapSHA256: strings.Repeat("0", 64)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if output, err := run(test.status, test.availability); err == nil {
				t.Fatalf("unverified bootstrap command succeeded: %s", output)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("unverified bootstrap executed: stat err=%v", err)
			}
		})
	}
}
