package auth

import "testing"

func TestForwardedIdentityAllowed(t *testing.T) {
	cases := []struct {
		name      string
		username  string
		groups    []string
		cloudMode bool
		want      bool
	}{
		// Outside Cloud: any group vocabulary, but never reserved principals.
		{"non-cloud: ordinary identity allowed", "alice@company.com", []string{"sre-team"}, false, true},
		{"non-cloud: unprefixed groups allowed", "alice", []string{"platform", "sre"}, false, true},
		{"non-cloud: prefixed system group allowed", "alice", []string{"oidc:system:masters"}, false, true},
		{"non-cloud: system:masters group rejected", "admin", []string{"sre-team", "system:masters"}, false, false},
		{"non-cloud: system username rejected", "system:kube-controller-manager", nil, false, false},
		{"non-cloud: serviceaccount username rejected", "system:serviceaccount:kube-system:default", nil, false, false},

		// Cloud: reserved-principal rejection.
		{"cloud: system:masters group rejected", "user_01ABC", []string{"kubeatlas:owner", "system:masters"}, true, false},
		{"cloud: system username rejected", "system:node:x", []string{"kubeatlas:owner"}, true, false},
		{"cloud: serviceaccount group rejected", "user_01ABC", []string{"system:serviceaccounts:kube-system"}, true, false},

		// Cloud: vocabulary allowlist.
		{"cloud: kubeatlas tier allowed", "user_01ABC", []string{"kubeatlas:owner", "kubeatlas:org:o1", "kubeatlas:user:u1"}, true, true},
		{"cloud: legacy cloud tier allowed", "user_01ABC", []string{"cloud:viewer", "cloud:org:o1"}, true, true},
		{"cloud: kubeatlas:idp group allowed", "user_01ABC", []string{"kubeatlas:idp:f81d4fae"}, true, true},
		{"cloud: kubeatlas:email allowed", "user_01ABC", []string{"kubeatlas:email:a@b.com"}, true, true},
		{"cloud: non-vocabulary group rejected", "user_01ABC", []string{"kubeatlas:owner", "sre-team"}, true, false},
		{"cloud: empty username rejected", "", []string{"kubeatlas:owner"}, true, false},
		{"cloud mode internal synthetic username allowed", "kubeatlas:system:alerts:o1", []string{"kubeatlas:owner"}, true, true},
		{"cloud: AI reader identity allowed", "kubeatlas:ai:o1", []string{"kubeatlas:ai:reader"}, true, true},
		{"cloud: no groups, valid user, allowed", "user_01ABC", nil, true, true},
		{"cloud: Hub owner group allowed", "user_01ABC", []string{"kubeatlas:hub:owner"}, true, true},
		{"cloud: Hub member group allowed", "user_01ABC", []string{"kubeatlas:hub:member"}, true, true},
		{"cloud: Hub viewer group allowed", "user_01ABC", []string{"kubeatlas:hub:viewer"}, true, true},
		{"cloud: unknown kubeatlas group rejected", "user_01ABC", []string{"kubeatlas:evil"}, true, false},
		{"non-cloud: unknown kubeatlas group unchanged", "alice", []string{"kubeatlas:evil"}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ForwardedIdentityAllowed(tc.username, tc.groups, tc.cloudMode)
			if got != tc.want {
				t.Errorf("ForwardedIdentityAllowed(%q, %v, cloud=%v) = %v, want %v",
					tc.username, tc.groups, tc.cloudMode, got, tc.want)
			}
		})
	}
}

func TestIsReservedPrincipal(t *testing.T) {
	reserved := []string{"system:masters", "system:authenticated", "system:node:x", "system:serviceaccount:kube-system:default", "system:serviceaccounts"}
	for _, p := range reserved {
		if !IsReservedPrincipal(p) {
			t.Errorf("IsReservedPrincipal(%q) = false, want true", p)
		}
	}
	// KubeAtlas's own namespaced synthetic principals must NOT be treated as reserved.
	notReserved := []string{"kubeatlas:system:alerts:o1", "kubeatlas:system", "kubeatlas:owner", "alice@company.com", "sre-team", ""}
	for _, p := range notReserved {
		if IsReservedPrincipal(p) {
			t.Errorf("IsReservedPrincipal(%q) = true, want false", p)
		}
	}
}
