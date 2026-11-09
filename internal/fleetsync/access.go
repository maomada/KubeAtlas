package fleetsync

import (
	"context"
	"slices"
)

type CredentialScope struct {
	ClusterIDs []string
	ReadOnly   bool
}

type credentialScopeKey struct{}

func WithCredentialScope(ctx context.Context, scope CredentialScope) context.Context {
	scope.ClusterIDs = slices.Clone(scope.ClusterIDs)
	return context.WithValue(ctx, credentialScopeKey{}, scope)
}

func CredentialScopeFromContext(ctx context.Context) (CredentialScope, bool) {
	scope, ok := ctx.Value(credentialScopeKey{}).(CredentialScope)
	scope.ClusterIDs = slices.Clone(scope.ClusterIDs)
	return scope, ok
}

func CredentialAllowsCluster(ctx context.Context, clusterID string) bool {
	scope, scoped := CredentialScopeFromContext(ctx)
	return !scoped || slices.Contains(scope.ClusterIDs, clusterID)
}
