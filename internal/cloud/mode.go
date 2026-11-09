package cloud

import (
	"log"
	"os"
	"strconv"
	"sync/atomic"
)

// cloudModeEnvVar is the boolean env var the Helm chart sets when
// `cloud.enabled: true`. We accept any value `strconv.ParseBool`
// recognizes (true / 1 / t / T / TRUE / True, and the false analogues)
// rather than the strict `== "true"` an earlier implementation used —
// a typo'd `True` or `1` silently degrading a Cloud deployment to OSS
// mode is a settings-leak liability (theme/pinnedKinds get persisted
// to the shared in-cluster Radar's settings.json instead of Cloud's
// per-user store).
const cloudModeEnvVar = "KUBEATLAS_CLOUD_MODE"

// warnedUnparseable rate-limits the unparseable-value warning to one
// emission per process so a malformed env var doesn't spam logs on
// every per-request capability check.
var warnedUnparseable atomic.Bool
var selfHostedMode atomic.Bool

// SetSelfHostedMode sets the process-local mode used by the self-hosted Hub
// source agent without changing the legacy environment contract.
func SetSelfHostedMode(enabled bool) {
	selfHostedMode.Store(enabled)
}

// Mode reports whether the process uses a Hub tunnel. The legacy env var is
// re-read on each call so tests using t.Setenv work as expected.
func Mode() bool {
	selfHosted := selfHostedMode.Load()
	raw, present := os.LookupEnv(cloudModeEnvVar)
	if !present || raw == "" {
		return selfHosted
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		// A set-but-unparseable env var is operator misconfiguration.
		// Default to false (OSS mode) but loudly so it surfaces in
		// chart-install logs rather than silently degrading multi-
		// tenant settings to a per-user store. CompareAndSwap ensures
		// we log only once per process even though Mode() is called
		// per request.
		if warnedUnparseable.CompareAndSwap(false, true) {
			log.Printf("[cloud] WARNING: %s=%q is not a valid bool — treating as false. Use 'true' or 'false'.", cloudModeEnvVar, raw)
		}
		return selfHosted
	}
	return selfHosted || parsed
}
