package traffic

import (
	"testing"

	"github.com/maomada/KubeAtlas/internal/bundledcharts"
)

func TestCarettaRecommendationUsesBundledChart(t *testing.T) {
	info := carettaHelmChart()
	if info.Repo != bundledcharts.Repository || info.RepoURL != "" || info.Version != bundledcharts.CarettaVersion {
		t.Fatalf("recommendation must use a pinned local bundle: %+v", info)
	}
	if _, _, err := bundledcharts.Load(info.ChartName, info.Version); err != nil {
		t.Fatal(err)
	}
}
