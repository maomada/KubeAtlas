package gitops

import "fmt"

func suspendedSetting(annotations map[string]string, names ...string) (bool, bool, error) {
	for _, name := range names {
		raw, found := annotations[name]
		if !found {
			continue
		}
		if raw != "true" && raw != "false" {
			return false, false, fmt.Errorf("invalid saved auto-sync setting")
		}
		// A current saved value is authoritative over stale older annotations,
		// matching the pre-branding resume contract.
		return raw == "true", true, nil
	}
	return false, false, nil
}
