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
