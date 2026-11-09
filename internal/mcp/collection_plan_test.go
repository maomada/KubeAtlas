package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/maomada/KubeAtlas/internal/k8s"
	"github.com/maomada/KubeAtlas/pkg/k8score"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
)

type collectionDiscoveryFixture struct {
	discovery.DiscoveryInterface
	lists []*metav1.APIResourceList
	err   error
	calls atomic.Int32
}

func (d *collectionDiscoveryFixture) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	d.calls.Add(1)
	return nil, d.lists, d.err
}

func TestCollectionPlanCompletenessAndPreferredVersion(t *testing.T) {
	all := []string{"get", "list", "watch"}
	read := []string{"get", "list"}
	resources := []k8s.APIResource{
		{Group: "", Version: "v1", Name: "pods", Kind: "Pod", Verbs: all},
		{Group: "", Version: "v1", Name: "secrets", Kind: "Secret", Verbs: all},
		{Group: "", Version: "v1", Name: "pods/status", Kind: "Pod", Verbs: all},
		{Group: "", Version: "v1", Name: "componentstatuses", Kind: "ComponentStatus", Verbs: read},
		{Group: "metrics.k8s.io", Version: "v1beta1", Name: "pods", Kind: "PodMetrics", Verbs: read},
		{Group: "example.io", Version: "v1", Name: "widgets", Kind: "Widget", Verbs: read},
		{Group: "example.io", Version: "v1beta1", Name: "widgets", Kind: "Widget", Verbs: all},
		{Group: "other.io", Version: "v1", Name: "widgets", Kind: "Widget", Verbs: all},
	}
	preferred := map[collectionKind]k8s.APIResource{}
	for _, resource := range resources {
		key := collectionKind{resource.Group, resource.Name}
		if _, exists := preferred[key]; !exists {
			preferred[key] = resource
		}
	}
	before, _ := json.Marshal(resources)
	for _, complete := range []bool{true, false} {
		plan := selectCollectionPlan(k8score.DiscoveryCatalog{Resources: resources, Complete: complete}, preferred)
		if plan.coverage.DiscoveryComplete != complete {
			t.Fatal("discovery completeness changed")
		}
		for _, want := range []collectionKind{{"", "pods"}, {"other.io", "widgets"}} {
			if !slices.Contains(plan.kinds, want) {
				t.Fatalf("missing supported %v", want)
			}
		}
		for _, absent := range []collectionKind{{"", "secrets"}, {"", "pods/status"}, {"", "componentstatuses"}, {"metrics.k8s.io", "pods"}, {"example.io", "widgets"}} {
			if slices.Contains(plan.kinds, absent) {
				t.Fatalf("unsupported inventory included: %v", absent)
			}
		}
		dra := collectionKind{"resource.k8s.io", "resourceclaims"}
		if slices.Contains(plan.kinds, dra) == complete {
			t.Fatal("missing DRA must be skipped only after complete discovery")
		}
		seen := map[string]bool{}
		for _, unsupported := range plan.coverage.UnsupportedKinds {
			key := unsupported.Group + "/" + unsupported.Version + "/" + unsupported.Resource
			if seen[key] {
				t.Fatal("duplicate unsupported kind")
			}
			seen[key] = true
			if !complete && unsupported.Reason == "api_not_served" {
				t.Fatal("incomplete discovery asserted absence")
			}
			if unsupported.Resource == "widgets" && (unsupported.Group != "example.io" || unsupported.Version != "v1" || unsupported.Reason != "missing_verbs:watch") {
				t.Fatalf("preferred/group mismatch: %+v", unsupported)
			}
		}
		if !seen["/v1/componentstatuses"] || !seen["metrics.k8s.io/v1beta1/pods"] || !seen["example.io/v1/widgets"] {
			t.Fatal("known missing verbs not reported")
		}
	}
	preferred[collectionKind{"example.io", "widgets"}] = resources[6]
	plan := selectCollectionPlan(k8score.DiscoveryCatalog{Resources: resources, Complete: true}, preferred)
	if !slices.Contains(plan.kinds, collectionKind{"example.io", "widgets"}) {
		t.Fatal("policy ignored resolved preferred version")
	}
	after, _ := json.Marshal(resources)
	if string(before) != string(after) {
		t.Fatal("selection mutated catalog or shared verbs")
	}
	legacy := selectCollectionPlan(k8score.DiscoveryCatalog{}, nil)
	if legacy.coverage.DiscoveryComplete || len(legacy.coverage.UnsupportedKinds) != 0 || !slices.Contains(legacy.kinds, collectionKind{"resource.k8s.io", "resourceclaims"}) || !strings.Contains(strings.Join(legacy.coverage.Limitations, " "), "unknown") {
		t.Fatal("missing discovery lost legacy fallback/unknown semantics")
	}
}

func TestCollectionPlanUnsupportedBoundAndDeterminism(t *testing.T) {
	catalog := k8score.DiscoveryCatalog{Complete: true}
	preferred := map[collectionKind]k8s.APIResource{}
	for i := 120; i >= 0; i-- {
		r := k8s.APIResource{Group: "example.io", Version: "v1", Name: fmt.Sprintf("kind-%03d", i), Kind: "Example", Verbs: []string{"get"}}
		catalog.Resources = append(catalog.Resources, r, r)
		preferred[collectionKind{r.Group, r.Name}] = r
	}
	a := selectCollectionPlan(catalog, preferred)
	slices.Reverse(catalog.Resources)
	b := selectCollectionPlan(catalog, preferred)
	if !reflect.DeepEqual(a, b) || len(a.coverage.UnsupportedKinds) != 100 || len(a.kinds) != 0 || !strings.Contains(strings.Join(a.coverage.Limitations, " "), "truncated to 100") {
		t.Fatal("unsupported metadata not bounded/deterministic or skipped kinds attempted")
	}
}

func TestCollectionPlanCachedDiscoveryNoRefresh(t *testing.T) {
	fixture := &collectionDiscoveryFixture{lists: []*metav1.APIResourceList{
		{GroupVersion: "example.io/v1", APIResources: []metav1.APIResource{{Name: "widgets", Kind: "Widget", Verbs: []string{"get", "list"}}}},
		{GroupVersion: "example.io/v1beta1", APIResources: []metav1.APIResource{{Name: "widgets", Kind: "Widget", Verbs: []string{"get", "list", "watch"}}}},
	}}
	restore, err := k8s.SetTestResourceDiscovery(fixture)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	d := k8s.GetResourceDiscovery()
	preferred, ok := d.GetResourceWithGroup("widgets", "example.io")
	if !ok {
		t.Fatal("fixture has no preferred entry")
	}
	before := fixture.calls.Load()
	plan := currentCollectionPlan()
	if fixture.calls.Load() != before {
		t.Fatal("collection planning refreshed discovery")
	}
	if slices.Contains(plan.kinds, collectionKind{"example.io", "widgets"}) != slices.Contains(preferred.Verbs, "watch") {
		t.Fatal("collection did not use actual native preferred entry")
	}
}
