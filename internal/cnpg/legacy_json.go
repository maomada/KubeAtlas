package cnpg

import (
	"encoding/json"
	"github.com/maomada/KubeAtlas/pkg/compat"
)

// UnmarshalJSON reads a typed pre-branding payload without changing resource maps.
func (value *CNPGReportIndex) UnmarshalJSON(data []byte) error {
	canonical, err := compat.CanonicalJSONFields(data, map[string]string{compat.LegacyVersionJSON: "kubeatlasVersion"})
	if err != nil {
		return err
	}
	type plain CNPGReportIndex
	var decoded plain
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		return err
	}
	*value = CNPGReportIndex(decoded)
	return nil
}
