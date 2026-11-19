package ai

import (
	"encoding/json"
	"github.com/maomada/KubeAtlas/pkg/compat"
)

// UnmarshalJSON reads a typed pre-branding payload without changing resource maps.
func (value *StepInfo) UnmarshalJSON(data []byte) error {
	canonical, err := compat.CanonicalJSONFields(data, map[string]string{compat.LegacyEvidenceJSON: "kubeatlasEvidence"})
	if err != nil {
		return err
	}
	type plain StepInfo
	var decoded plain
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		return err
	}
	*value = StepInfo(decoded)
	return nil
}
