package auth

import (
	"github.com/maomada/KubeAtlas/pkg/compat"
	"strings"
)

// Identity filtering runs wherever KubeAtlas accepts an identity it will later
// impersonate: proxy headers, OIDC login, and session cookies. The SA's
// `impersonate` grant is cluster-scoped and K8s RBAC can't restrict it to a
// prefix, so KubeAtlas enforces the constraint where it accepts identities. It
// does not constrain anyone holding the SA's token directly.
//
// Every mode rejects Kubernetes-reserved principals. Under KubeAtlas Cloud the
// control plane is the asserting party over the tunnel, so KubeAtlas additionally
// requires the Cloud identity vocabulary.

// reservedPrincipalPrefixes are Kubernetes-reserved identity namespaces an
// impersonated identity must never assert. Blocking them removes the
// worst-case escalation ceiling (system:masters, the kubelet/node identity,
// any ServiceAccount). It does NOT contain an identity source that asserts an
// ordinary user or group bound to powerful RBAC, and is deliberately not sold
// as such.
var reservedPrincipalPrefixes = []string{
	"system:", // system:masters, system:authenticated, system:nodes, system:serviceaccounts, …
}

// IsReservedPrincipal reports whether a username or group falls in a reserved
// Kubernetes namespace. Matched as an exact leading prefix so KubeAtlas's own
// namespaced synthetic principals (e.g. "kubeatlas:system:alerts:…") are
// unaffected — only leading "system:" is reserved.
func IsReservedPrincipal(principal string) bool {
	for _, p := range reservedPrincipalPrefixes {
		if strings.HasPrefix(principal, p) {
			return true
		}
	}
	return false
}

// kubeatlasVocabularyPrefixes are the group namespaces the KubeAtlas Cloud control
// plane legitimately injects. A compromised hub can still assert an ordinary
// kubeatlas: tier, but cannot introduce identities outside the vocabulary the
// chart binds.
var kubeatlasVocabularyPrefixes = []string{"kubeatlas:", compat.LegacyTierPrefix, "cloud:"}

// IsKubeAtlasVocabularyGroup reports whether a group is in the KubeAtlas Cloud group
// vocabulary (kubeatlas:* canonical or cloud:* legacy).
func IsKubeAtlasVocabularyGroup(group string) bool {
	for _, p := range kubeatlasVocabularyPrefixes {
		if strings.HasPrefix(group, p) {
			return true
		}
	}
	return false
}

// ForwardedIdentityAllowed decides whether an identity may be impersonated. It
// enforces:
//
//   - reserved-principal rejection (every mode): username and every group must
//     be outside the reserved K8s namespaces (blocks system:masters etc.);
//   - vocabulary allowlist (Cloud only): username must be a Cloud identity and
//     every group must be a defined tier, service identity or owned identity
//     metadata field. Unknown reserved groups never become Kubernetes grants.
//
// Returns false to reject the whole identity — never silently drops values,
// which would quietly change what the user is authorized to do.
func ForwardedIdentityAllowed(username string, groups []string, cloudMode bool) bool {
	if IsReservedPrincipal(username) {
		return false
	}
	for _, g := range groups {
		if IsReservedPrincipal(g) {
			return false
		}
	}
	if !cloudMode {
		return true
	}
	if !isCloudUsername(username) {
		return false
	}
	for _, g := range groups {
		if !isForwardedCloudGroup(g) {
			return false
		}
	}
	return true
}

// Keep reservation detection broad, but accepted forwarded vocabulary finite.
// Org/user/IdP/email claims are the existing metadata namespaces, not role names.
func isForwardedCloudGroup(group string) bool {
	if group == "kubeatlas:hub:owner" || group == "kubeatlas:hub:member" || group == "kubeatlas:hub:viewer" {
		return true
	}
	for _, prefix := range kubeatlasVocabularyPrefixes {
		if !strings.HasPrefix(group, prefix) {
			continue
		}
		value := strings.TrimPrefix(group, prefix)
		switch value {
		case "owner", "member", "viewer":
			return true
		case "system", "ai:reader":
			return prefix != "cloud:"
		}
		for _, field := range []string{"org:", "user:", "idp:", "email:"} {
			if strings.HasPrefix(value, field) && len(value) > len(field) {
				return true
			}
		}
		return false
	}
	return false
}

// isCloudUsername reports whether a username is one the KubeAtlas Cloud control
// plane legitimately injects: the opaque per-user id the hub sets as
// X-Forwarded-User, or an internal synthetic principal namespaced under
// "kubeatlas:system:" or the AI username "kubeatlas:ai:<org>". The hub never forwards
// a K8s-reserved username, so rejecting reserved is the meaningful check; the
// opaque WorkOS id has no fixed prefix to match beyond "non-empty and not reserved".
func isCloudUsername(username string) bool {
	return username != "" && !IsReservedPrincipal(username)
}
