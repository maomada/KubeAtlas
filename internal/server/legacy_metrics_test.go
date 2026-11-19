package server

import (
	"github.com/maomada/KubeAtlas/pkg/compat"
	"github.com/prometheus/client_golang/prometheus"
	"testing"
)

func TestMetricNameRotationPreservesDeployedRecordingRules(t *testing.T) {
	r := prometheus.NewRegistry()
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "kubeatlas_fixture_value", Help: "Fixture value"})
	g.Set(7)
	r.MustRegister(g)
	families, err := (metricsWithLegacyNames{r}).Gather()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	for _, family := range families {
		values[family.GetName()] = family.Metric[0].GetGauge().GetValue()
	}
	if len(values) != 2 || values["kubeatlas_fixture_value"] != 7 || values[compat.LegacyMetricsPrefix+"fixture_value"] != 7 {
		t.Fatalf("metric aliases=%v", values)
	}
}
