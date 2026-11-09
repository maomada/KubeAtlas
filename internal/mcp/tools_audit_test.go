package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/internal/auditcontext"
	"github.com/maomada/KubeAtlas/internal/k8s"
	"github.com/maomada/KubeAtlas/internal/settings"
	bp "github.com/maomada/KubeAtlas/pkg/audit"
	"github.com/maomada/KubeAtlas/pkg/checks"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCollectAuditToolFindingsUsesCanonicalSeverity(t *testing.T) {
	raw := []bp.Finding{
		{
			Kind: "Deployment", Namespace: "prod", Name: "api",
			CheckID: "privileged", Category: bp.CategorySecurity,
			Severity: bp.SeverityDanger, Message: "privileged",
		},
		{
			Kind: "Deployment", Namespace: "prod", Name: "api",
			CheckID: "cpuRequestMissing", Category: bp.CategoryEfficiency,
			Severity: bp.SeverityWarning, Message: "missing CPU request",
		},
	}

	categories, findings := collectAuditToolFindings(raw, bp.CheckRegistry, "", "")
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(findings))
	}
	if findings[0].Severity != checks.SeverityHigh || findings[1].Severity != checks.SeverityMedium {
		t.Fatalf("severities = [%q, %q], want [high, medium]", findings[0].Severity, findings[1].Severity)
	}

	summary := summarizeAuditToolFindings(findings, categories)
	if summary.Critical != 0 || summary.High != 1 || summary.Medium != 1 || summary.Low != 0 {
		t.Fatalf("summary = %+v, want zero critical/low and one high/medium", summary)
	}
	if summary.Resources != 1 {
		t.Fatalf("resources = %d, want 1", summary.Resources)
	}
}

func TestAuditSettingsAgreeBetweenResourceAndAggregate(t *testing.T) {
	var objects []runtime.Object
	for _, ns := range []string{"app", "kube-system", "hero-system", "hidden"} {
		objects = append(objects, &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: ns},
			Spec: appsv1.DeploymentSpec{
				Replicas: new(int32(1)),
				Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v1"}}}},
			},
		})
	}
	if err := k8s.InitTestResourceCache(fake.NewClientset(objects...)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k8s.ResetTestState)
	t.Cleanup(func() { settings.SetOperatorConfig(nil) })
	ctx := withTestUserPerms(t, "audit-settings-reader", nil, []string{"app", "kube-system", "hero-system"})
	for _, tc := range []struct {
		name     string
		config   *settings.AuditConfig
		ignored  bool
		disabled bool
	}{
		{name: "defaults", ignored: true},
		{name: "disabled check", config: &settings.AuditConfig{IgnoredNamespaces: settings.DefaultAuditConfig().IgnoredNamespaces, DisabledChecks: []string{"singleReplica"}}, ignored: true, disabled: true},
		{name: "explicit empty settings", config: &settings.AuditConfig{IgnoredNamespaces: []string{}, DisabledChecks: []string{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings.SetOperatorConfig(&settings.OperatorConfig{Version: 1, Audit: tc.config})
			for _, ns := range []string{"app", "kube-system", "hero-system"} {
				raw, _, err := handleGetAudit(ctx, nil, auditInput{Namespace: ns, Limit: 100})
				if err != nil {
					t.Fatal(err)
				}
				var aggregate auditToolResult
				if err := json.Unmarshal([]byte(extractText(t, raw)), &aggregate); err != nil {
					t.Fatal(err)
				}
				summary, detail := auditcontext.SummarizeResource(k8s.GetResourceCache(), "apps", "Deployment", ns, "api", auditOptions(ctx))
				if summary == nil || summary.Scope == nil || aggregate.Scope == nil {
					t.Fatal("missing audit scope")
				}
				if !reflect.DeepEqual(summary.Scope.DisabledChecks, aggregate.Scope.DisabledChecks) || !reflect.DeepEqual(summary.Scope.IgnoredNamespacePatterns, aggregate.Scope.IgnoredNamespacePatterns) {
					t.Fatal("settings scope diverged")
				}
				if tc.ignored && ns != "app" {
					if summary.Scope.Status != "skipped" || !slices.Contains(summary.Scope.Reasons, "ignored_namespace") || !slices.Contains(aggregate.Scope.SkippedNamespaces, ns) {
						t.Fatalf("ignored namespace not explicit: resource=%+v aggregate=%+v", summary.Scope, aggregate.Scope)
					}
					if len(detail) != 0 || summary.Count != 0 || len(aggregate.Findings) != 0 || aggregate.TotalCount != 0 || aggregate.Summary.Resources != 0 {
						t.Fatalf("ignored findings survived: %+v %+v", summary, aggregate)
					}
					continue
				}
				if summary.Scope.Status != "checked" || len(detail) == 0 || summary.Count != len(detail) {
					t.Fatalf("missing checked resource findings: %+v", summary)
				}
				if aggregate.Truncated || aggregate.TotalCount != len(aggregate.Findings) || aggregate.TotalCount != len(detail) {
					t.Fatalf("aggregate counts diverged: %+v", aggregate)
				}
				seenSingleReplica := false
				for _, finding := range detail {
					if finding.CheckID == "singleReplica" {
						seenSingleReplica = true
					}
					if !slices.ContainsFunc(aggregate.Findings, func(other auditFinding) bool {
						return other.Resource == "Deployment/"+ns+"/api" && other.Check == finding.CheckID && other.Severity == checks.MapSeverity(finding.Severity) && other.Category == finding.Category && other.Message == finding.Message
					}) {
						t.Fatalf("resource finding absent from aggregate: %+v", finding)
					}
				}
				if seenSingleReplica == tc.disabled {
					t.Fatalf("singleReplica presence = %v, disabled = %v", seenSingleReplica, tc.disabled)
				}
				if tc.disabled && !slices.Contains(summary.Scope.Reasons, "disabled_checks") {
					t.Fatal("disabled checks not explained")
				}
			}
			raw, _, err := handleGetAudit(ctx, nil, auditInput{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			var all auditToolResult
			if err := json.Unmarshal([]byte(extractText(t, raw)), &all); err != nil {
				t.Fatal(err)
			}
			for _, f := range all.Findings {
				if strings.Contains(f.Resource, "/hidden/") || tc.disabled && f.Check == "singleReplica" || tc.ignored && (strings.Contains(f.Resource, "/hero-system/") || strings.Contains(f.Resource, "/kube-system/")) {
					t.Fatalf("excluded finding leaked: %+v", f)
				}
			}
			if slices.Contains(all.Scope.SkippedNamespaces, "hidden") {
				t.Fatal("hidden namespace leaked in settings scope")
			}
		})
	}
}

func TestCollectAuditToolFindingsFiltersCanonicalSeverity(t *testing.T) {
	raw := []bp.Finding{
		{
			Kind: "Pod", Namespace: "prod", Name: "api",
			CheckID: "privileged", Category: bp.CategorySecurity, Severity: bp.SeverityDanger,
		},
		{
			Kind: "Pod", Namespace: "prod", Name: "api",
			CheckID: "cpuRequestMissing", Category: bp.CategoryEfficiency, Severity: bp.SeverityWarning,
		},
	}

	for _, tc := range []struct {
		filter checks.Severity
		want   checks.Severity
	}{
		{filter: checks.SeverityHigh, want: checks.SeverityHigh},
		{filter: checks.SeverityMedium, want: checks.SeverityMedium},
	} {
		t.Run(string(tc.filter), func(t *testing.T) {
			categories, findings := collectAuditToolFindings(raw, bp.CheckRegistry, "", tc.filter)
			if len(findings) != 1 || findings[0].Severity != tc.want {
				t.Fatalf("findings = %+v, want one %s finding", findings, tc.want)
			}
			if len(categories) != 1 || categories[findings[0].Category] != 1 {
				t.Fatalf("categories = %+v, want only the category available at severity %s", categories, tc.filter)
			}
		})
	}
}

func TestParseAuditSeverityRejectsLegacyAndUnknownValues(t *testing.T) {
	for _, value := range []string{"danger", "warning", "urgent"} {
		_, err := parseAuditSeverity(value)
		if err == nil {
			t.Fatalf("parseAuditSeverity(%q) succeeded, want clear contract error", value)
		}
		if !strings.Contains(err.Error(), "critical, high, medium, low") {
			t.Fatalf("parseAuditSeverity(%q) error = %q, want canonical values", value, err)
		}
	}
}

func TestGetClusterAuditRejectsLegacySeverityBeforeClusterAccess(t *testing.T) {
	_, _, err := handleGetAudit(context.Background(), nil, auditInput{Severity: "danger"})
	if err == nil || !strings.Contains(err.Error(), "critical, high, medium, low") {
		t.Fatalf("legacy severity error = %v, want canonical contract error", err)
	}
}

func TestAuditSummaryAlwaysEmitsAllSeverityKeys(t *testing.T) {
	data, err := json.Marshal(auditSummary{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, key := range []string{`"critical":0`, `"high":0`, `"medium":0`, `"low":0`} {
		if !strings.Contains(got, key) {
			t.Fatalf("zero summary = %s, missing %s", got, key)
		}
	}
	if strings.Contains(got, `"warning"`) || strings.Contains(got, `"danger"`) {
		t.Fatalf("zero summary exposes raw severity vocabulary: %s", got)
	}
}
