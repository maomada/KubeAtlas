package helm

import (
	"github.com/maomada/KubeAtlas/pkg/compat"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"strings"
	"testing"
)

func TestDeployedChartSourceLabelsRetainTheirIntegrityBinding(t *testing.T) {
	source := ChartSourceCandidate{Type: "repository", Reference: "fixture", URL: "https://charts.example.test"}
	current := chartSourceLabels(&source)
	old := map[string]string{}
	for key, value := range current {
		switch {
		case key == chartSourceTypeLabel:
			old[compat.LegacyChartSourceType] = value
		case strings.HasPrefix(key, chartSourceRefLabel):
			old[compat.LegacyChartSourceRefPrefix+strings.TrimPrefix(key, chartSourceRefLabel)] = value
		case strings.HasPrefix(key, chartSourceURLLabel):
			old[compat.LegacyChartSourceURLPrefix+strings.TrimPrefix(key, chartSourceURLLabel)] = value
		}
	}
	ch := &chart.Chart{Metadata: &chart.Metadata{Name: "fixture", Version: "1.0.0"}}
	old[compat.LegacyChartSourceBinding] = chartSourceBinding(ch)
	rel := &release.Release{Labels: old, Chart: ch}
	got, confirmed := confirmedChartSource(rel)
	if !confirmed || got == nil || *got != source {
		t.Fatal("old source binding lost")
	}
	ch.Metadata.Version = "2.0.0"
	if _, confirmed := confirmedChartSource(rel); confirmed {
		t.Fatal("old binding accepted changed chart")
	}
	old[chartSourceTypeLabel] = "oci"
	if _, valid := chartSourceFromRelease(rel); valid {
		t.Fatal("conflicting source metadata accepted")
	}
}
