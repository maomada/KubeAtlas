package server

import (
	"github.com/maomada/KubeAtlas/pkg/compat"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"
	"strings"
)

// Existing recording rules and canonical names share exactly the same sample.
type metricsWithLegacyNames struct{ prometheus.Gatherer }

func (g metricsWithLegacyNames) Gather() ([]*dto.MetricFamily, error) {
	families, err := g.Gatherer.Gather()
	if err != nil {
		return families, err
	}
	result := append([]*dto.MetricFamily(nil), families...)
	for _, family := range families {
		if family == nil || !strings.HasPrefix(family.GetName(), "kubeatlas_") {
			continue
		}
		alias := proto.Clone(family).(*dto.MetricFamily)
		name := compat.LegacyMetricsPrefix + strings.TrimPrefix(family.GetName(), "kubeatlas_")
		alias.Name = &name
		result = append(result, alias)
	}
	return result, nil
}
