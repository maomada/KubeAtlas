package hub

import (
	"context"
	"github.com/maomada/KubeAtlas/pkg/compat"
	"testing"
)

func TestPrincipalContextDoesNotAliasScope(t *testing.T) {
	if _, ok := PrincipalFromContext(context.Background()); ok {
		t.Fatal("unauthenticated context acquired a principal")
	}
	input := Principal{CredentialID: "credential", UserID: "user", Groups: []string{"kubeatlas:viewer"}, ClusterIDs: []string{"alpha"}, ReadOnly: true}
	ctx := ContextWithPrincipal(context.Background(), input)
	input.Groups[0], input.ClusterIDs[0] = "kubeatlas:owner", "beta"
	first, ok := PrincipalFromContext(ctx)
	if !ok || first.CredentialID != "credential" || first.UserID != "user" || !first.ReadOnly || first.Groups[0] != "kubeatlas:viewer" || first.ClusterIDs[0] != "alpha" {
		t.Fatal("stored scope changed with caller slices")
	}
	first.Groups[0], first.ClusterIDs[0] = "kubeatlas:owner", "beta"
	second, _ := PrincipalFromContext(ctx)
	if second.Groups[0] != "kubeatlas:viewer" || second.ClusterIDs[0] != "alpha" {
		t.Fatal("retrieved scope aliases request identity")
	}
}

func TestPrincipalReservedTierNamespacesRemainClosed(t *testing.T) {
	for _, prefix := range []string{"kubeatlas:hub:", "kubeatlas:", compat.LegacyTierPrefix, "cloud:"} {
		for _, role := range []string{"owner", "member", "viewer"} {
			p := Principal{UserID: "fixture", Groups: []string{prefix + role}, ClusterIDs: []string{"alpha"}}
			if !principalAllowed(p, "alpha") {
				t.Fatalf("known tier rejected:%s%s", prefix, role)
			}
		}
		invalid := [][]string{{prefix + "viewer", prefix + "owner"}, {prefix + "org:"}}
		if prefix == "kubeatlas:" || prefix == "kubeatlas:hub:" {
			invalid = append(invalid, []string{prefix + "evil"}, []string{prefix + "owner", prefix + "evil"})
		}
		for _, groups := range invalid {
			p := Principal{UserID: "fixture", Groups: groups, ClusterIDs: []string{"alpha"}}
			if principalAllowed(p, "alpha") {
				t.Fatalf("unknown or conflicting reserved tier accepted:%v", groups)
			}
		}
	}
	for _, pair := range [][2]string{{"kubeatlas:system:fixture", "kubeatlas:system"}, {compat.LegacySystemUserPrefix + "fixture", compat.LegacySystemGroup}, {"kubeatlas:ai:fixture", "kubeatlas:ai:reader"}, {compat.LegacyAIUserPrefix + "fixture", compat.LegacyAIReaderGroup}} {
		if !principalAllowed(Principal{UserID: pair[0], Groups: []string{pair[1]}, ClusterIDs: []string{"alpha"}}, "alpha") {
			t.Fatal("verified service identity rejected")
		}
	}
	if principalAllowed(Principal{UserID: "fixture", Groups: []string{"system:masters"}, ClusterIDs: []string{"alpha"}}, "alpha") {
		t.Fatal("Kubernetes reserved group accepted")
	}
}

func TestPrincipalIdentityMetadataIsNotAnUnknownRole(t *testing.T) {
	for _, prefix := range []string{"kubeatlas:", compat.LegacyTierPrefix, "cloud:"} {
		for _, field := range []string{"org:", "user:", "idp:", "email:"} {
			metadata := prefix + field + "fixture"
			for _, groups := range [][]string{{metadata}, {metadata, "kubeatlas:hub:viewer"}} {
				p := Principal{UserID: "fixture", Groups: groups, ClusterIDs: []string{"alpha"}}
				if !principalAllowed(p, "alpha") {
					t.Fatalf("defined metadata rejected:%v", groups)
				}
			}
			if principalAllowed(Principal{UserID: "fixture", Groups: []string{prefix + field, "kubeatlas:hub:viewer"}, ClusterIDs: []string{"alpha"}}, "alpha") {
				t.Fatal("empty identity metadata accepted")
			}
		}
	}
}

func TestPrincipalDeployedCustomGroupsRemainOpaque(t *testing.T) {
	for _, prefix := range []string{compat.LegacyTierPrefix, "cloud:"} {
		for _, groups := range [][]string{{prefix + "custom-readers"}, {prefix + "custom-readers", "kubeatlas:hub:viewer"}} {
			if !principalAllowed(Principal{UserID: "fixture", Groups: groups, ClusterIDs: []string{"alpha"}}, "alpha") {
				t.Fatalf("deployed custom group rejected:%v", groups)
			}
		}
		if principalAllowed(Principal{UserID: "fixture", Groups: []string{prefix + "custom-readers", "kubeatlas:hub:viewer", prefix + "owner"}, ClusterIDs: []string{"alpha"}}, "alpha") {
			t.Fatal("opaque group bypassed conflicting-tier check")
		}
	}
}
