package hub

import (
	"context"
	"slices"
)

type principalContextKey struct{}

// ContextWithPrincipal must follow authentication on every HTTP request; it
// does not establish or extend a bearer credential's validity.
func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	principal.Groups = slices.Clone(principal.Groups)
	principal.ClusterIDs = slices.Clone(principal.ClusterIDs)
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	principal.Groups = slices.Clone(principal.Groups)
	principal.ClusterIDs = slices.Clone(principal.ClusterIDs)
	return principal, ok
}
