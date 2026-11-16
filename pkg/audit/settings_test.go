package audit

import "testing"

func TestIgnoredNamespaceMatcherPreservesSettingsSemantics(t *testing.T) {
	for _, tc := range []struct {
		pattern, namespace string
		want               bool
	}{
		{"kube-system", "kube-system", true},
		{"kube-system", "kube-system-extra", false},
		{"*-system", "hero-system", true},
		{"*-system", "system-hero", false},
		{"dev-*", "dev-app", true},
		{"dev-*", "app-dev", false},
		{"*", "app", true},
		{"*", "", true},
		{"", "", true},
		{"dev*app", "dev-app", false},
		{"dev*app", "dev*app", true},
		{"*system*", "hero-system", false},
		{"*system*", "system-app", false},
	} {
		t.Run(tc.pattern+"/"+tc.namespace, func(t *testing.T) {
			if got := IgnoredNamespaceMatcher([]string{tc.pattern})(tc.namespace); got != tc.want {
				t.Fatalf("match = %v, want %v", got, tc.want)
			}
			raw := &ScanResults{Findings: []Finding{{Namespace: tc.namespace, Kind: "Pod", Name: "api", CheckID: "runAsRoot", Category: CategorySecurity, Severity: SeverityDanger}}, EvaluatedByNamespace: map[string]map[string]int{"runAsRoot": {tc.namespace: 1}}}
			filtered := ApplySettings(raw, []string{tc.pattern}, nil)
			if (len(filtered.Findings) == 0) != tc.want {
				t.Fatal("filter differs from matcher")
			}
			if _, ok := filtered.EvaluatedByNamespace["runAsRoot"][tc.namespace]; ok == tc.want {
				t.Fatal("evaluation denominator differs from matcher")
			}
		})
	}
}
