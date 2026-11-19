package auth

import (
	"context"
	"github.com/maomada/KubeAtlas/pkg/compat"
	"strings"
)

// CloudRole captures the user's tier under KubeAtlas Cloud's RBAC model.
// Cloud injects one of `kubeatlas:owner` / `kubeatlas:member` / `kubeatlas:viewer`
// into X-Forwarded-Groups (plus the deprecated `cloud:*` equivalents for
// the transition window); this type extracts that signal.
//
// CloudRole gates KubeAtlas's own features — settings, integrations, self-upgrade —
// which run as KubeAtlas's service account, so nothing else checks them. It must
// not gate operations that run as the impersonated user (Helm, probe pods):
// Kubernetes RBAC decides those, and when IdP groups grant access the role
// says nothing about what the user may do in the cluster.
type CloudRole string

const (
	// RoleNone means the request is not from a Cloud-attributed user
	// (a standalone deployment). Authenticated Cloud users with no recognized
	// tier are RoleDenied. AtLeast
	// returns true for RoleNone so non-Cloud deploys aren't gated.
	RoleNone   CloudRole = ""
	RoleDenied CloudRole = "denied"
	RoleViewer CloudRole = "viewer"
	RoleMember CloudRole = "member"
	RoleOwner  CloudRole = "owner"
)

// kubeatlas: is the canonical vocabulary; cloud: is the pre-rename legacy still
// emitted by the hub (and possibly present from older hubs) during the
// deprecation window. Accepting both keeps every hub/binary version pairing
// working; drop cloud: in the next major.
var tierGroupPrefixes = []string{"kubeatlas:hub:", "kubeatlas:", compat.LegacyTierPrefix, "cloud:"}

// ErrCodeCloudRoleInsufficient is the stable wire value emitted in 403
// response bodies (`error_code` field) when a request is denied by a
// Cloud role gate. frontend + MCP clients branch on this exact string —
// never rename. Hoisted out of internal/helm/handlers.go so the
// canonical value lives next to the role types it pertains to.
const ErrCodeCloudRoleInsufficient = "cloud_role_insufficient"

var tierRank = map[CloudRole]int{
	RoleViewer: 1,
	RoleMember: 2,
	RoleOwner:  3,
}

// CloudRoleFromGroups returns the highest-ranked Cloud tier present in
// the group list (accepting both the canonical kubeatlas:<tier> and legacy
// cloud:<tier> forms). Returns RoleNone when no tier group is found,
// which the call sites treat as "not running under Cloud."
func CloudRoleFromGroups(groups []string) CloudRole {
	var best CloudRole
	bestRank := 0
	for _, g := range groups {
		for _, prefix := range tierGroupPrefixes {
			if !strings.HasPrefix(g, prefix) {
				continue
			}
			role := CloudRole(strings.TrimPrefix(g, prefix))
			rank, ok := tierRank[role]
			if !ok {
				continue
			}
			if rank > bestRank {
				best = role
				bestRank = rank
			}
			break
		}
	}
	return best
}

// CloudRoleFromContext is a convenience over UserFromContext +
// CloudRoleFromGroups. Returns RoleNone when no user is in context.
func CloudRoleFromContext(ctx context.Context) CloudRole {
	user := UserFromContext(ctx)
	if user == nil {
		return RoleNone
	}
	if user.Cloud && user.System {
		// User-token mode keeps HubGroups empty for verified service identities.
		// Their dedicated Kubernetes identity permits reads, never owner actions.
		if IsSystemIdentity(user.Username, user.Groups) {
			return RoleViewer
		}
		return RoleDenied
	}
	groups := user.Groups
	if user.HubGroups != nil {
		groups = user.HubGroups
	}
	role := CloudRoleFromGroups(groups)
	if user.Cloud && role == RoleNone {
		if IsSystemIdentity(user.Username, groups) {
			return RoleViewer
		}
		return RoleDenied
	}
	return role
}

// AtLeast reports whether r meets or exceeds min. RoleNone (no Cloud
// role detected) bypasses the gate — non-Cloud deploys must not be
// blocked by Cloud-tier checks.
func (r CloudRole) AtLeast(min CloudRole) bool {
	if r == RoleNone {
		return true
	}
	return tierRank[r] >= tierRank[min]
}

// String returns the role name suitable for log/error messages.
func (r CloudRole) String() string {
	if r == RoleNone {
		return "none"
	}
	return string(r)
}
