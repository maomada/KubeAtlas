package auth

import (
	"context"
	"testing"
)

func TestCloudRoleFromGroupsKubeAtlasVocabulary(t *testing.T) {
	cases := []struct {
		name   string
		groups []string
		want   CloudRole
	}{
		{"kubeatlas canonical", []string{"kubeatlas:member", "kubeatlas:org:o1", "kubeatlas:user:u1"}, RoleMember},
		{"dual-emit picks tier once", []string{"kubeatlas:viewer", "kubeatlas:org:o1", "cloud:viewer", "cloud:org:o1"}, RoleViewer},
		{"legacy cloud only (old hub)", []string{"cloud:owner", "cloud:org:o1"}, RoleOwner},
		{"idp groups are not tiers", []string{"kubeatlas:idp:owner", "kubeatlas:idp:member"}, RoleNone},
		{"highest rank wins across vocabularies", []string{"cloud:viewer", "kubeatlas:owner"}, RoleOwner},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CloudRoleFromGroups(tc.groups); got != tc.want {
				t.Errorf("CloudRoleFromGroups(%v) = %q, want %q", tc.groups, got, tc.want)
			}
		})
	}
}

func TestCloudRoleFromGroups(t *testing.T) {
	cases := []struct {
		name   string
		groups []string
		want   CloudRole
	}{
		{"empty groups", nil, RoleNone},
		{"no KubeAtlas prefix", []string{"developers", "sre"}, RoleNone},
		{"viewer", []string{"kubeatlas:viewer"}, RoleViewer},
		{"member", []string{"kubeatlas:member"}, RoleMember},
		{"owner", []string{"kubeatlas:owner"}, RoleOwner},
		{"unknown KubeAtlas tier", []string{"kubeatlas:superuser"}, RoleNone},
		{"viewer + unrelated group", []string{"kubeatlas:viewer", "team:platform"}, RoleViewer},
		// Cloud injects multiple kubeatlas:* groups (kubeatlas:<tier>, kubeatlas:org:<id>,
		// kubeatlas:user:<id>). Only the tier should drive the role.
		{"multiple KubeAtlas groups", []string{"kubeatlas:viewer", "kubeatlas:org:abc", "kubeatlas:user:xyz"}, RoleViewer},
		// If two tiers are present (shouldn't happen in practice), prefer
		// the highest. Defensive: a header-stuffing attempt to downgrade
		// shouldn't succeed by adding a lower-tier alongside.
		{"two tiers — pick highest", []string{"kubeatlas:viewer", "kubeatlas:owner"}, RoleOwner},
		{"member + viewer", []string{"kubeatlas:member", "kubeatlas:viewer"}, RoleMember},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CloudRoleFromGroups(tc.groups)
			if got != tc.want {
				t.Errorf("CloudRoleFromGroups(%v) = %q, want %q", tc.groups, got, tc.want)
			}
		})
	}
}

func TestCloudRole_AtLeast(t *testing.T) {
	cases := []struct {
		role CloudRole
		min  CloudRole
		want bool
	}{
		// RoleNone bypasses — non-Cloud deploys must not be gated.
		{RoleNone, RoleViewer, true},
		{RoleNone, RoleMember, true},
		{RoleNone, RoleOwner, true},
		{RoleViewer, RoleViewer, true},
		{RoleViewer, RoleMember, false},
		{RoleViewer, RoleOwner, false},
		{RoleMember, RoleViewer, true},
		{RoleMember, RoleMember, true},
		{RoleMember, RoleOwner, false},
		{RoleOwner, RoleViewer, true},
		{RoleOwner, RoleMember, true},
		{RoleOwner, RoleOwner, true},
	}
	for _, tc := range cases {
		got := tc.role.AtLeast(tc.min)
		if got != tc.want {
			t.Errorf("CloudRole(%q).AtLeast(%q) = %v, want %v", tc.role, tc.min, got, tc.want)
		}
	}
}

func TestCloudRoleFromContext(t *testing.T) {
	t.Run("no user in context", func(t *testing.T) {
		if got := CloudRoleFromContext(context.Background()); got != RoleNone {
			t.Errorf("CloudRoleFromContext(empty) = %q, want RoleNone", got)
		}
	})
	t.Run("user with Cloud groups", func(t *testing.T) {
		ctx := ContextWithUser(context.Background(), &User{
			Username: "alice",
			Groups:   []string{"kubeatlas:member", "kubeatlas:org:abc"},
		})
		if got := CloudRoleFromContext(ctx); got != RoleMember {
			t.Errorf("CloudRoleFromContext = %q, want member", got)
		}
	})
	t.Run("user without Cloud groups (OSS proxy/oidc)", func(t *testing.T) {
		ctx := ContextWithUser(context.Background(), &User{
			Username: "bob",
			Groups:   []string{"sre", "platform-team"},
		})
		if got := CloudRoleFromContext(ctx); got != RoleNone {
			t.Errorf("CloudRoleFromContext = %q, want RoleNone (no kubeatlas: prefix)", got)
		}
	})
}
