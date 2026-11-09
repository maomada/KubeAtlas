package mcp

import (
	"cmp"
	"slices"
	"strings"

	"github.com/maomada/KubeAtlas/internal/fleetsync"
	"github.com/maomada/KubeAtlas/internal/k8s"
	"github.com/maomada/KubeAtlas/pkg/k8score"
	"github.com/maomada/KubeAtlas/pkg/resourceid"
)

type collectionKind struct{ group, resource string }

type collectionPlan struct {
	kinds    []collectionKind
	coverage fleetsync.CollectionCoverage
}

func currentCollectionPlan() collectionPlan {
	discovery := k8s.GetResourceDiscovery()
	if discovery == nil {
		return selectCollectionPlan(k8score.DiscoveryCatalog{}, nil)
	}
	catalog := discovery.Catalog()
	preferred := make(map[collectionKind]k8s.APIResource)
	for i, resource := range catalog.Resources {
		catalog.Resources[i].Verbs = slices.Clone(resource.Verbs)
		key := collectionKind{resource.Group, resource.Name}
		if _, exists := preferred[key]; exists {
			continue
		}
		if resolved, ok := discovery.GetResourceWithGroup(resource.Name, resource.Group); ok && resolved.Group == resource.Group && resolved.Name == resource.Name {
			resolved.Verbs = slices.Clone(resolved.Verbs)
			preferred[key] = resolved
		}
	}
	return selectCollectionPlan(catalog, preferred)
}

// The catalog is served-version evidence; preferred is the exact entry native
// reads resolve. Combining verbs across versions would admit unsupported reads.
func selectCollectionPlan(catalog k8score.DiscoveryCatalog, preferred map[collectionKind]k8s.APIResource) collectionPlan {
	plan := collectionPlan{coverage: fleetsync.CollectionCoverage{
		DiscoveryComplete: catalog.Complete,
		UnsupportedKinds:  []fleetsync.UnsupportedKind{},
		Limitations: []string{
			"Optional snapshot-collection support only; not a restriction on direct/native live tools, including metrics tools.",
			"Cached API discovery is not an inventory, health check, or per-request Kubernetes RBAC evaluation. Secrets and subresources are excluded.",
		},
	}}
	if !catalog.Complete {
		plan.coverage.Limitations = append(plan.coverage.Limitations, "API discovery is incomplete or unavailable; missing kinds are unknown. Legacy built-in collection attempts remain; undiscovered kinds may be missing.")
	}
	candidates := make(map[collectionKind]k8s.APIResource)
	present := make(map[collectionKind]bool)
	for _, builtin := range resourceid.Builtins {
		candidates[collectionKind{builtin.Group, builtin.Resource}] = k8s.APIResource{Group: builtin.Group, Version: builtin.Version, Name: builtin.Resource, Kind: builtin.Kind}
	}
	for _, resource := range catalog.Resources {
		key := collectionKind{resource.Group, resource.Name}
		candidates[key], present[key] = resource, true
	}
	for key, resource := range candidates {
		if key.resource == "" || strings.Contains(key.resource, "/") || strings.EqualFold(key.resource, "secrets") || strings.EqualFold(resource.Kind, "Secret") {
			continue
		}
		reason := ""
		if !present[key] {
			if catalog.Complete {
				reason = "api_not_served"
			}
		} else {
			resolved, ok := preferred[key]
			if !ok || resolved.Group != key.group || resolved.Name != key.resource {
				plan.coverage.Limitations = appendOnce(plan.coverage.Limitations, "Some discovered kinds could not be resolved to a preferred read version; their snapshot collection is omitted and support remains unknown.")
				continue
			}
			resource = resolved
			var missing []string
			for _, verb := range []string{"get", "list", "watch"} {
				if !slices.Contains(resource.Verbs, verb) {
					missing = append(missing, verb)
				}
			}
			if len(missing) > 0 {
				reason = "missing_verbs:" + strings.Join(missing, ",")
			}
		}
		if reason != "" {
			plan.coverage.UnsupportedKinds = append(plan.coverage.UnsupportedKinds, fleetsync.UnsupportedKind{Group: resource.Group, Version: resource.Version, Resource: resource.Name, Kind: resource.Kind, Reason: reason})
			continue
		}
		plan.kinds = append(plan.kinds, key)
	}
	slices.SortFunc(plan.kinds, func(a, b collectionKind) int {
		return cmp.Or(strings.Compare(a.group, b.group), strings.Compare(a.resource, b.resource))
	})
	slices.SortFunc(plan.coverage.UnsupportedKinds, func(a, b fleetsync.UnsupportedKind) int {
		return cmp.Or(strings.Compare(a.Group, b.Group), strings.Compare(a.Version, b.Version), strings.Compare(a.Resource, b.Resource))
	})
	if len(plan.coverage.UnsupportedKinds) > 0 {
		plan.coverage.Limitations = append(plan.coverage.Limitations, "Known unsupported snapshot inventories are skipped, not emitted as repeated error documents.")
	}
	if len(plan.coverage.UnsupportedKinds) > 100 {
		plan.coverage.UnsupportedKinds = plan.coverage.UnsupportedKinds[:100]
		plan.coverage.Limitations = append(plan.coverage.Limitations, "Unsupported-kind metadata truncated to 100 entries; further known unsupported kinds are also skipped.")
	}
	return plan
}
