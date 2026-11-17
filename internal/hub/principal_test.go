package hub

import (
	"context"
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
