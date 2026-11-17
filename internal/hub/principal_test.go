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
		for _, groups := range [][]string{{prefix + "evil"}, {prefix + "owner", prefix + "evil"}, {prefix + "viewer", prefix + "owner"}} {
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
