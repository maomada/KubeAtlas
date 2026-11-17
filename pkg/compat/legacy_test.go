package compat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentHeaderCompatibilityRejectsAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name           string
		values         http.Header
		want           string
		present, valid bool
	}{
		{"new", http.Header{AgentCredentialsHeader: {"impersonate"}}, "impersonate", true, true},
		{"old", http.Header{LegacyAgentCredentialsHeader: {"impersonate"}}, "impersonate", true, true},
		{"matching", http.Header{AgentCredentialsHeader: {"impersonate"}, LegacyAgentCredentialsHeader: {"impersonate"}}, "impersonate", true, true},
		{"conflict", http.Header{AgentCredentialsHeader: {"impersonate"}, LegacyAgentCredentialsHeader: {"user-token"}}, "", true, false},
		{"duplicate", http.Header{LegacyAgentCredentialsHeader: {"impersonate", "impersonate"}}, "", true, false},
		{"case-duplicate", http.Header{LegacyAgentCredentialsHeader: {"impersonate"}, "x-radar-kubernetes-credentials": {"impersonate"}}, "", true, false},
		{"absent", http.Header{}, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, present, valid := SingleHeader(tc.values, AgentCredentialsHeader, LegacyAgentCredentialsHeader)
			if value != tc.want || present != tc.present || valid != tc.valid {
				t.Fatalf("header result=%q/%v/%v", value, present, valid)
			}
		})
	}
}

func TestCookieRotationRejectsDuplicateAndMixedNames(t *testing.T) {
	for _, names := range [][]string{{LegacyPortalCookie}, {"kubeatlas_portal_session"}, {LegacyPortalCookie, "kubeatlas_portal_session"}, {LegacyPortalCookie, LegacyPortalCookie}} {
		r := httptest.NewRequest("GET", "http://localhost", nil)
		for _, name := range names {
			r.AddCookie(&http.Cookie{Name: name, Value: "same"})
		}
		_, err := SingleCookie(r, "kubeatlas_portal_session", LegacyPortalCookie)
		if (err == nil) != (len(names) == 1) {
			t.Fatalf("names%v err=%v", names, err)
		}
	}
}

func TestOwnedMetadataAndTypedJSONDoNotOverrideConflicts(t *testing.T) {
	labels, ok := CanonicalChartSourceLabels(map[string]string{LegacyChartSourceType: "repository", LegacyChartSourceRefPrefix + "0": "ABC"})
	if !ok || labels["kubeatlas.io/chart-source-type"] != "repository" {
		t.Fatal("old owned metadata lost")
	}
	if _, ok := CanonicalChartSourceLabels(map[string]string{LegacyChartSourceType: "repository", "kubeatlas.io/chart-source-type": "oci"}); ok {
		t.Fatal("conflicting source accepted")
	}
	if _, err := CanonicalJSONFields([]byte(`{"radarEvidence":true,"kubeatlasEvidence":false}`), map[string]string{LegacyEvidenceJSON: "kubeatlasEvidence"}); err == nil {
		t.Fatal("conflicting proof flag accepted")
	}
	data, err := CanonicalJSONFields([]byte(`{"radarVersion":"old","resource":{"radarVersion":"user-owned"}}`), map[string]string{LegacyVersionJSON: "kubeatlasVersion"})
	if err != nil || string(data) != `{"kubeatlasVersion":"old","resource":{"radarVersion":"user-owned"}}` {
		t.Fatalf("typed adaptation changed resource data:%s %v", data, err)
	}
}

func TestDeployedAgentReceivesTierGateWithoutReplacingKubernetesGroups(t *testing.T) {
	for _, tier := range []string{"owner", "member", "viewer"} {
		user, groups, valid := OutboundIdentity("kubeatlas:fixture", []string{"custom", "kubeatlas:hub:" + tier})
		if !valid || user != "kubeatlas:fixture" || len(groups) != 3 || groups[0] != "custom" || groups[1] != "kubeatlas:hub:"+tier || groups[2] != LegacyTierPrefix+tier {
			t.Fatalf("outbound identity=%s %v", user, groups)
		}
	}
	for _, groups := range [][]string{nil, {"custom"}, {"kubeatlas:hub:unknown"}, {"kubeatlas:hub:viewer", "kubeatlas:owner"}, {"kubeatlas:system", "kubeatlas:hub:viewer"}} {
		if _, _, valid := OutboundIdentity("fixture", groups); valid {
			t.Fatalf("unknown or conflicting tiers accepted:%v", groups)
		}
	}
}
func TestOwnedRestoreMetadataReadsLegacyAndRejectsConflict(t *testing.T) {
	for _, legacy := range []string{LegacyCNPGRestoreValidation, LegacyCNPGDetachedUID} {
		canonical := "kubeatlas.io/" + strings.TrimPrefix(legacy, "radar.skyhook.io/")
		if got, ok := OwnedValue(map[string]string{legacy: "fixture"}, canonical, legacy); !ok || got != "fixture" {
			t.Fatal("legacy owned metadata lost")
		}
		if _, ok := OwnedValue(map[string]string{legacy: "fixture", canonical: "different"}, canonical, legacy); ok {
			t.Fatal("conflicting owned metadata accepted")
		}
	}
}
func TestTimelineSQLRetainsDeployedPhysicalSchema(t *testing.T) {
	if got := PhysicalTimelineSQL("SELECT * FROM kubeatlas_timeline_events; SELECT * FROM kubeatlas_timeline_schema_migrations"); got != "SELECT * FROM radar_timeline_events; SELECT * FROM radar_timeline_schema_migrations" {
		t.Fatal("physical schema changed")
	}
}

func TestDeployedBackgroundIdentityRetainsExactPair(t *testing.T) {
	user, groups, ok := OutboundIdentity("kubeatlas:system:fixture", []string{"kubeatlas:system"})
	if !ok || user != LegacySystemUserPrefix+"fixture" || len(groups) != 1 || groups[0] != LegacySystemGroup {
		t.Fatal("exact background identity lost")
	}
}

func TestIdentityMetadataDoesNotBecomeATierOrChangeKubernetesGroups(t *testing.T) {
	for _, prefix := range []string{"kubeatlas:", LegacyTierPrefix, "cloud:"} {
		for _, field := range []string{"org:", "user:", "idp:", "email:"} {
			metadata := prefix + field + "fixture"
			if !IsIdentityMetadataGroup(metadata) {
				t.Fatalf("defined metadata rejected:%s", metadata)
			}
			original := []string{metadata, "custom-k8s-group", "kubeatlas:hub:viewer"}
			user, groups, ok := OutboundIdentity("fixture", original)
			if !ok || user != "fixture" || len(groups) != 4 || groups[0] != metadata || groups[1] != original[1] || groups[2] != original[2] || groups[3] != LegacyTierPrefix+"viewer" {
				t.Fatalf("metadata or Kubernetes group changed:%v", groups)
			}
			if original[0] != metadata || len(original) != 3 {
				t.Fatal("input groups mutated")
			}
			if IsIdentityMetadataGroup(prefix + field) {
				t.Fatal("empty identity metadata accepted")
			}
			if _, _, ok := OutboundIdentity("fixture", []string{prefix + field, "kubeatlas:hub:viewer"}); ok {
				t.Fatal("empty metadata plus tier accepted")
			}
		}
		if IsIdentityMetadataGroup(prefix + "evil") {
			t.Fatal("unknown field classified as metadata")
		}
		if prefix == "kubeatlas:" {
			if _, _, ok := OutboundIdentity("fixture", []string{prefix + "evil", "kubeatlas:hub:viewer"}); ok {
				t.Fatal("unknown canonical group plus tier accepted")
			}
		}
	}
}

func TestDeployedOpaqueGroupsStayUnchangedAndNeverBecomeTiers(t *testing.T) {
	for _, prefix := range []string{LegacyTierPrefix, "cloud:"} {
		old := prefix + "custom-reader/team"
		if !IsLegacyOpaqueIdentityGroup(old) {
			t.Fatal("deployed custom group lost")
		}
		original := []string{old, "kubeatlas:hub:viewer"}
		_, groups, ok := OutboundIdentity("fixture", original)
		if !ok || len(groups) != 3 || groups[0] != old || groups[1] != original[1] || groups[2] != LegacyTierPrefix+"viewer" {
			t.Fatalf("deployed group rewritten:%v", groups)
		}
		if _, _, ok := OutboundIdentity("fixture", []string{old}); ok {
			t.Fatal("opaque group created a missing-role bypass")
		}
		if _, _, ok := OutboundIdentity("fixture", []string{old, "kubeatlas:hub:viewer", prefix + "owner"}); ok {
			t.Fatal("opaque group bypassed contradictory-tier guard")
		}
		for _, value := range []string{"", "org:", "idp:", "owner:bad", "system:bad", "ai:owner", "custom,owner", "custom\nowner"} {
			if IsLegacyOpaqueIdentityGroup(prefix + value) {
				t.Fatalf("malformed owned or header metadata accepted:%q", value)
			}
		}
	}
	if IsLegacyOpaqueIdentityGroup("kubeatlas:custom-reader") {
		t.Fatal("new canonical group acquired legacy broad scope")
	}
}
