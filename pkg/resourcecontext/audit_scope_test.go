package resourcecontext

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuditSummarySkippedScopeSurvivesJSON(t *testing.T) {
	summary := AuditSummary{Scope: &AuditScope{Status: "skipped", Reasons: []string{"ignored_namespace"}, IgnoredNamespacePatterns: []string{"*-system"}, SkippedNamespaces: []string{"hero-system"}, DisabledChecks: []string{"singleReplica"}}}
	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"count":0`, `"status":"skipped"`, `"reasons":["ignored_namespace"]`, `"skippedNamespaces":["hero-system"]`, `"disabledChecks":["singleReplica"]`} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("missing %s in %s", key, data)
		}
	}
	if strings.Contains(string(data), "highestSeverity") || strings.Contains(string(data), "topFinding") {
		t.Fatalf("skipped result has finding metadata: %s", data)
	}
}
