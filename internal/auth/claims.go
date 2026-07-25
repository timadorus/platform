package auth

import "context"

// Claims holds the subset of JWT claims handlers need. Authorization policy beyond "is this
// token valid" is out of scope for v1 (plan §13) — handlers that need it can read Roles.
type Claims struct {
	Subject string
	Roles   []string
}

type claimsKey struct{}

func WithClaims(ctx context.Context, claims Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, claims)
}

// FromContext returns the claims stashed by the auth middleware, if any.
func FromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(claimsKey{}).(Claims)
	return claims, ok
}
