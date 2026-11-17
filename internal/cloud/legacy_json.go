package cloud

import (
	"encoding/json"
	"github.com/maomada/KubeAtlas/pkg/compat"
)

// UnmarshalJSON reads a typed pre-branding payload without changing resource maps.
func (value *ConnectMetadata) UnmarshalJSON(data []byte) error {
	canonical, err := compat.CanonicalJSONFields(data, map[string]string{compat.LegacyVersionSnakeJSON: "kubeatlas_version"})
	if err != nil {
		return err
	}
	type plain ConnectMetadata
	var decoded plain
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		return err
	}
	*value = ConnectMetadata(decoded)
	return nil
}

func (value *EnrollRequest) UnmarshalJSON(data []byte) error {
	canonical, err := compat.CanonicalJSONFields(data, map[string]string{compat.LegacyVersionSnakeJSON: "kubeatlas_version"})
	if err != nil {
		return err
	}
	type plain EnrollRequest
	var decoded plain
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		return err
	}
	*value = EnrollRequest(decoded)
	return nil
}
