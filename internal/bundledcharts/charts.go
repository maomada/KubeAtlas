package bundledcharts

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
)

const Repository = "kubeatlas-bundled"
const CarettaVersion = "0.0.16"

//go:embed caretta-0.0.16.tgz
var carettaArchive []byte

type Source struct {
	Name       string
	Version    string
	Repository string
	URL        string
	Digest     string
}

func Load(name, version string) (*chart.Chart, Source, error) {
	source := Source{
		Name: "caretta", Version: CarettaVersion,
		Repository: "groundcover", URL: "https://helm.groundcover.com/",
		Digest: "24593ec0e9e7971520b2ab3f049d7fceebe0519a04f857f711ca5c7505e0af72",
	}
	if name != source.Name || (version != "" && version != "latest" && version != source.Version) {
		return nil, Source{}, fmt.Errorf("bundled chart %q version %q is unavailable; available: caretta %s (no external fallback)", name, version, CarettaVersion)
	}
	loaded, err := loadArchive(carettaArchive, source)
	return loaded, source, err
}

func loadArchive(data []byte, source Source) (*chart.Chart, error) {
	if fmt.Sprintf("%x", sha256.Sum256(data)) != source.Digest {
		return nil, fmt.Errorf("bundled chart %s failed SHA-256 verification", source.Name)
	}
	loaded, err := loader.LoadArchive(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("load bundled chart: %w", err)
	}
	if loaded.Metadata.Name != source.Name || loaded.Metadata.Version != source.Version {
		return nil, fmt.Errorf("bundled chart metadata does not match its catalog entry")
	}
	return loaded, nil
}
