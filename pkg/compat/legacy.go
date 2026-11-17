// Package compat contains explicit readers for deployed pre-branding contracts.
// New application values use KubeAtlas names; these bytes remain for upgrades.
package compat

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
)

var ErrAmbiguousCookie = errors.New("ambiguous cookie names")

const (
	AgentVersionHeader           = "X-KubeAtlas-Version"
	AgentCredentialsHeader       = "X-KubeAtlas-Kubernetes-Credentials"
	LegacyAgentVersionHeader     = "X-Radar-Version"
	LegacyAgentCredentialsHeader = "X-Radar-Kubernetes-Credentials"
	LegacyAgentSelfUpgradeHeader = "X-Radar-Self-Upgrade-Available"
	LegacyAgentNamespaceHeader   = "X-Radar-Namespace"
	LegacyAgentReleaseHeader     = "X-Radar-Release"
	LegacyAgentAPIServerHeader   = "X-Radar-API-Server-URL"
	LegacyTierPrefix             = "radar:"
	LegacySystemUserPrefix       = "radar:system:"
	LegacySystemGroup            = "radar:system"
	LegacyAIUserPrefix           = "radar:ai:"
	LegacyAIReaderGroup          = "radar:ai:reader"
	LegacyPortalCookie           = "radar_portal_session"
	LegacySessionCookie          = "radar_session"
	LegacyOIDCStateCookie        = "radar_oidc_state"
	LegacyOIDCVerifierCookie     = "radar_oidc_verifier"
	LegacyOIDCForceLoginCookie   = "radar_force_login"
	LegacyArgoPruneAnnotation    = "radarhq.io/suspended-prune"
	LegacyArgoSelfHealAnnotation = "radarhq.io/suspended-selfheal"
	LegacyArgoValidationInfo     = "radar-validation-id"
	LegacyArgoInitiator          = "radar"
	LegacyChartSourceBinding     = "radarhq.io/chart-source-binding"
	LegacyChartSourceAttempt     = "radarhq.io/chart-source-attempt"
	LegacyChartSourceType        = "radarhq.io/chart-source-type"
	LegacyChartSourceRefPrefix   = "radarhq.io/chart-source-ref-"
	LegacyChartSourceURLPrefix   = "radarhq.io/chart-source-url-"
	LegacyIgnoreChecksAnnotation = "radarhq.io/ignore-checks"
	LegacyDebugNodeLabel         = "radarhq.io/debug-node"
	OlderDebugNodeLabel          = "radar.skyhook.io/debug-node"
	LegacyManagedBy              = "radar"
	LegacyContainerName          = "radar"
	LegacyChartName              = "radar"
	LegacyCloudSecretName        = "radar-cloud-config"
	LegacyCloudInstallAttempt    = "radarhq.io/cloud-install-attempt"
	LegacyCNPGRestoreValidation  = "radar.skyhook.io/restore-validation"
	LegacyCNPGDetachedUID        = "radar.skyhook.io/detached-cluster-uid"
	LegacyHealthSource           = "radar"
	LegacyCheckSource            = "radar_builtin"
	LegacyMCPToolPrefix          = "mcp__radar__"
	LegacyDottedToolPrefix       = "radar."
	LegacyUnderscoreToolPrefix   = "radar_"
	EvidenceNamespacePattern     = "(?:kubeatlas|radar)"
	LegacyVersionJSON            = "radarVersion"
	LegacyVersionSnakeJSON       = "radar_version"
	LegacyEvidenceJSON           = "radarEvidence"
	LegacyEvidenceRefPrefix      = "[[radar:evidence-ref="
	LegacyMetricsPrefix          = "radar_"
	LegacyAppVersionPrefix       = "radar-app-v"
	LegacyTimelineEpochHeader    = "X-Radar-Timeline-Epoch"
	LegacyTimelineMaxHeader      = "X-Radar-Timeline-Max-Seq"
	LegacyTimelineMinHeader      = "X-Radar-Timeline-Min-Seq"
	LegacyResourceReadHeader     = "X-Radar-Resource-Read"
)

// SingleHeader accepts one old name, one new name, or matching single values.
// It rejects duplicates (including differently cased map keys) and conflicts.
func SingleHeader(headers http.Header, canonical, legacy string) (string, bool, bool) {
	var current, old []string
	for name, values := range headers {
		switch {
		case strings.EqualFold(name, canonical):
			current = append(current, values...)
		case strings.EqualFold(name, legacy):
			old = append(old, values...)
		}
	}
	if len(current) > 1 || len(old) > 1 {
		return "", true, false
	}
	if len(current) == 1 && len(old) == 1 && current[0] != old[0] {
		return "", true, false
	}
	if len(current) == 1 {
		return current[0], true, true
	}
	if len(old) == 1 {
		return old[0], true, true
	}
	return "", false, true
}

func IsLegacyIdentityHeader(name string) bool {
	switch strings.ToLower(name) {
	case "x-radar-user", "x-radar-groups", "x-radar-role", "x-radar-roles", "x-radar-identity":
		return true
	}
	return false
}

// OwnedValue never lets one spelling override conflicting owned metadata.
func OwnedValue(values map[string]string, canonical, legacy string) (string, bool) {
	current, hasCurrent := values[canonical]
	old, hasOld := values[legacy]
	if hasCurrent && hasOld && current != old {
		return "", false
	}
	if hasCurrent {
		return current, true
	}
	return old, true
}

// SingleCookie prevents ambiguous sessions across a name rotation. A caller
// must still verify the returned cookie cryptographically or in its session store.
func SingleCookie(r *http.Request, names ...string) (*http.Cookie, error) {
	var found *http.Cookie
	for _, cookie := range r.Cookies() {
		for _, name := range names {
			if cookie.Name == name {
				if found != nil {
					return nil, ErrAmbiguousCookie
				}
				found = cookie
			}
		}
	}
	if found == nil {
		return nil, http.ErrNoCookie
	}
	return found, nil
}

// PhysicalTimelineSQL preserves the existing database schema and lock domain.
// It applies only to our own SQL text, never user resource JSON or SQL values.
func PhysicalTimelineSQL(query string) string {
	return strings.NewReplacer(
		"kubeatlas_timeline_", "radar_timeline_",
	).Replace(query)
}

func CanonicalReadReason(value string) string {
	switch value {
	case "radar_forbidden":
		return "kubeatlas_forbidden"
	case "radar_credentials_rejected":
		return "kubeatlas_credentials_rejected"
	case "radar_namespace_not_collected":
		return "kubeatlas_namespace_not_collected"
	case "radar_kind_not_collected":
		return "kubeatlas_kind_not_collected"
	case "radar_config":
		return "kubeatlas_config"
	}
	return value
}

// OutboundIdentity keeps the deployed cluster RBAC vocabulary until Agents and
// their bindings are upgraded together. Existing kubeatlas:hub roles are stable.
func OutboundIdentity(username string, groups []string) (string, []string, bool) {
	// Background identities must remain exact pairs; no human may acquire one
	// by adding a reserved group to a normal principal.
	if len(groups) == 1 {
		for _, pair := range [][4]string{{"kubeatlas:system:", "kubeatlas:system", LegacySystemUserPrefix, LegacySystemGroup}, {"kubeatlas:ai:", "kubeatlas:ai:reader", LegacyAIUserPrefix, LegacyAIReaderGroup}} {
			if strings.HasPrefix(username, pair[0]) && len(username) > len(pair[0]) && groups[0] == pair[1] {
				return pair[2] + strings.TrimPrefix(username, pair[0]), []string{pair[3]}, true
			}
			if strings.HasPrefix(username, pair[2]) && len(username) > len(pair[2]) && groups[0] == pair[3] {
				return username, append([]string(nil), groups...), true
			}
		}
	}
	tier := ""
	for _, group := range groups {
		for _, prefix := range []string{"kubeatlas:hub:", "kubeatlas:", LegacyTierPrefix, "cloud:"} {
			if !strings.HasPrefix(group, prefix) {
				continue
			}
			candidate := strings.TrimPrefix(group, prefix)
			if candidate != "owner" && candidate != "member" && candidate != "viewer" {
				return "", nil, false
			}
			if tier != "" && candidate != tier {
				return "", nil, false
			}
			tier = candidate
			break
		}
	}
	if tier == "" {
		return "", nil, false
	}

	for _, pair := range [][2]string{{"kubeatlas:system:", LegacySystemUserPrefix}, {"kubeatlas:ai:", LegacyAIUserPrefix}} {
		if strings.HasPrefix(username, pair[0]) {
			username = pair[1] + strings.TrimPrefix(username, pair[0])
			break
		}
	}
	out := make([]string, len(groups))
	for i, group := range groups {
		out[i] = group
		switch group {
		case "kubeatlas:hub:owner", "kubeatlas:hub:member", "kubeatlas:hub:viewer":
			// Keep the Kubernetes group and also supply the old Agent tier gate.
			out = append(out, LegacyTierPrefix+strings.TrimPrefix(group, "kubeatlas:hub:"))
		case "kubeatlas:owner", "kubeatlas:member", "kubeatlas:viewer", "kubeatlas:system", "kubeatlas:ai:reader":
			out[i] = LegacyTierPrefix + strings.TrimPrefix(group, "kubeatlas:")
		}
	}
	return username, out, true
}

func CanonicalChartSourceLabels(values map[string]string) (map[string]string, bool) {
	aliases := [][2]string{
		{LegacyChartSourceBinding, "kubeatlas.io/chart-source-binding"},
		{LegacyChartSourceAttempt, "kubeatlas.io/chart-source-attempt"},
		{LegacyChartSourceType, "kubeatlas.io/chart-source-type"},
		{LegacyChartSourceRefPrefix, "kubeatlas.io/chart-source-ref-"},
		{LegacyChartSourceURLPrefix, "kubeatlas.io/chart-source-url-"},
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	for key, value := range values {
		for _, alias := range aliases {
			if key == alias[0] || strings.HasSuffix(alias[0], "-") && strings.HasPrefix(key, alias[0]) {
				canonical := alias[1] + strings.TrimPrefix(key, alias[0])
				if existing, found := out[canonical]; found && existing != value {
					return nil, false
				}
				out[canonical] = value
			}
		}
	}
	return out, true
}

func IsLegacyChartSourceLabel(key string) bool {
	return key == LegacyChartSourceBinding || key == LegacyChartSourceAttempt || key == LegacyChartSourceType || strings.HasPrefix(key, LegacyChartSourceRefPrefix) || strings.HasPrefix(key, LegacyChartSourceURLPrefix)
}

// CanonicalJSONFields adapts only explicitly named top-level DTO fields.
// Nested resource payloads and user-supplied map keys are never traversed.
func CanonicalJSONFields(data []byte, aliases map[string]string) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	for old, canonical := range aliases {
		value, found := object[old]
		if !found {
			continue
		}
		if current, exists := object[canonical]; exists {
			var a, b any
			if json.Unmarshal(value, &a) != nil || json.Unmarshal(current, &b) != nil || !reflect.DeepEqual(a, b) {
				return nil, fmt.Errorf("conflicting compatibility fields for %s", canonical)
			}
		} else {
			object[canonical] = value
		}
		delete(object, old)
	}
	return json.Marshal(object)
}

func IsProductMCPTool(value string) bool {
	return strings.HasPrefix(value, "mcp__kubeatlas__") || strings.HasPrefix(value, LegacyMCPToolPrefix)
}
func CanonicalToolName(value string) string {
	for _, prefix := range []string{"mcp__kubeatlas__", LegacyMCPToolPrefix, "kubeatlas_", LegacyUnderscoreToolPrefix, "kubeatlas.", LegacyDottedToolPrefix} {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return value
}
