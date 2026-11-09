package auditcontext

import (
	"slices"

	"github.com/maomada/KubeAtlas/internal/settings"
	bpaudit "github.com/maomada/KubeAtlas/pkg/audit"
	"github.com/maomada/KubeAtlas/pkg/resourcecontext"
)

// SettingsScope reports exclusions only for namespaces already visible to the caller.
// checked means a scan ran with these settings, not that all checks passed.
func SettingsScope(cfg settings.AuditConfig, namespaces []string) *resourcecontext.AuditScope {
	scope := &resourcecontext.AuditScope{
		Status:                   "checked",
		IgnoredNamespacePatterns: slices.Clone(cfg.IgnoredNamespaces),
		DisabledChecks:           slices.Clone(cfg.DisabledChecks),
	}
	matches := bpaudit.IgnoredNamespaceMatcher(cfg.IgnoredNamespaces)
	for _, ns := range namespaces {
		if matches(ns) {
			scope.SkippedNamespaces = append(scope.SkippedNamespaces, ns)
		}
	}
	slices.Sort(scope.SkippedNamespaces)
	scope.SkippedNamespaces = slices.Compact(scope.SkippedNamespaces)
	if len(scope.SkippedNamespaces) > 0 {
		scope.Reasons = append(scope.Reasons, "ignored_namespace")
	}
	if len(scope.DisabledChecks) > 0 {
		scope.Reasons = append(scope.Reasons, "disabled_checks")
	}
	return scope
}
