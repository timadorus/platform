// Package auth verifies JWT bearer tokens and wires that verification into the
// oapi-codegen-generated servers' request validation middleware (see middleware.go). The
// identity provider/issuer itself is out of scope (plan §10) — the caller supplies a key set
// (JWKS or a single static key) plus the expected issuer/audience via config.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// acceptableClockSkew bounds how far apart this server's clock and the token issuer's clock
// may drift and still have exp/nbf/iat checks pass. Kept intentionally small — this is
// tolerance for real clock drift between servers, not a way to paper over misconfiguration.
const acceptableClockSkew = 60 * time.Second

var errEmptyToken = errors.New("auth: empty bearer token")

// Verifier verifies signed JWT bearer tokens against a key set. Pluggability = the key set,
// issuer, and audience all come from config (internal/config), so swapping identity
// providers needs no code change.
type Verifier struct {
	keySet   jwk.Set
	issuer   string
	audience string
}

func NewVerifier(keySet jwk.Set, issuer, audience string) *Verifier {
	return &Verifier{keySet: keySet, issuer: issuer, audience: audience}
}

// FetchJWKS fetches a JWKS document from url, for constructing a Verifier from an identity
// provider's published key set.
func FetchJWKS(ctx context.Context, url string) (jwk.Set, error) {
	set, err := jwk.Fetch(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("auth: fetch JWKS from %s: %w", url, err)
	}
	return set, nil
}

// NewStaticSecretKeySet builds a single-key symmetric (HMAC) key set for local
// development/testing when no real identity provider is configured. kid must match the
// "kid" header on tokens verified against this set — jwx requires a matching kid by design
// (see jwt.WithKeySet docs), so dev-issued test tokens must set the same kid.
func NewStaticSecretKeySet(kid string, secret []byte) (jwk.Set, error) {
	key, err := jwk.Import(secret)
	if err != nil {
		return nil, fmt.Errorf("auth: import static secret key: %w", err)
	}
	if err := key.Set(jwk.KeyIDKey, kid); err != nil {
		return nil, fmt.Errorf("auth: set kid on static secret key: %w", err)
	}
	if err := key.Set(jwk.AlgorithmKey, "HS256"); err != nil {
		return nil, fmt.Errorf("auth: set alg on static secret key: %w", err)
	}
	set := jwk.NewSet()
	if err := set.AddKey(key); err != nil {
		return nil, fmt.Errorf("auth: add static secret key to set: %w", err)
	}
	return set, nil
}

// Verify parses and validates raw as a signed JWT, checking the signature against the
// configured key set plus (if configured) issuer and audience, and returns the claims
// handlers need.
//
// Algorithm-confusion / "alg: none" defense: this is structural, not a separate check here.
// jwt.WithKeySet requires every key in the set to carry an explicit "alg" (see
// NewStaticSecretKeySet, and JWKS documents from a real IdP normally set this per key too),
// and jwx refuses to verify using a key whose alg doesn't match the token's header — the
// token's own header is never trusted to pick the algorithm. A bare unsigned ("none") token
// has no matching key by construction and is rejected the same way a wrong-signature token
// is: as a verification failure below, not a special case.
func (v *Verifier) Verify(ctx context.Context, raw string) (Claims, error) {
	if raw == "" {
		return Claims{}, errEmptyToken
	}

	// ValidateOption implements ParseOption, so issuer/audience/context/skew checks can be
	// passed straight to Parse alongside the key set.
	parseOpts := []jwt.ParseOption{
		jwt.WithKeySet(v.keySet),
		jwt.WithContext(ctx),
		jwt.WithAcceptableSkew(acceptableClockSkew),
	}
	if v.issuer != "" {
		parseOpts = append(parseOpts, jwt.WithIssuer(v.issuer))
	}
	if v.audience != "" {
		parseOpts = append(parseOpts, jwt.WithAudience(v.audience))
	}

	token, err := jwt.Parse([]byte(raw), parseOpts...)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: verify token: %w", err)
	}

	subject, _ := token.Subject()
	var roles []string
	_ = token.Get("roles", &roles) // optional custom claim; absence is not an error

	return Claims{Subject: subject, Roles: roles}, nil
}
