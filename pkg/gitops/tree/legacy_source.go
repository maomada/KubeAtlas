package tree

import (
	"encoding/json"
	"github.com/maomada/KubeAtlas/pkg/compat"
)

func (source *HealthSource) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value == compat.LegacyHealthSource {
		value = string(HealthSourceKubeAtlas)
	}
	*source = HealthSource(value)
	return nil
}
