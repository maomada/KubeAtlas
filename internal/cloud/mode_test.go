package cloud

import (
	"os"
	"testing"
)

func TestModeCombinesProcessAndLegacyState(t *testing.T) {
	SetSelfHostedMode(false)
	t.Cleanup(func() { SetSelfHostedMode(false) })

	t.Setenv(cloudModeEnvVar, "false")
	if Mode() {
		t.Fatal("legacy false unexpectedly enabled Hub mode")
	}

	SetSelfHostedMode(true)
	if !Mode() {
		t.Fatal("process-local self-hosted mode was not reported")
	}
	if got := os.Getenv(cloudModeEnvVar); got != "false" {
		t.Fatalf("legacy environment changed to %q", got)
	}

	SetSelfHostedMode(false)
	if Mode() {
		t.Fatal("self-hosted mode remained enabled after clearing process state")
	}

	t.Setenv(cloudModeEnvVar, "true")
	if !Mode() {
		t.Fatal("legacy true no longer enables compatibility mode")
	}
}

func TestModeAcceptsLegacyBooleanSpellings(t *testing.T) {
	SetSelfHostedMode(false)
	t.Cleanup(func() { SetSelfHostedMode(false) })
	for _, value := range []string{"true", "1", "T", "TRUE"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(cloudModeEnvVar, value)
			if !Mode() {
				t.Fatalf("Mode() = false for legacy value %q", value)
			}
		})
	}
}
