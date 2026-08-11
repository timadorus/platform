// Package auth verifies JWT bearer tokens and wires that verification into the
// oapi-codegen-generated servers' request validation middleware (see middleware.go). The
// identity provider/issuer itself is out of scope (plan §10) — the caller supplies a key set
// (JWKS or a single static key) plus the expected issuer/audience via config.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
// provider's published key set. If hostHeader is non-empty, the outgoing HTTP request's Host
// header is overridden to it while still dialing url's own host:port.
//
// hostHeader exists for multi-tenant identity providers (Zitadel confirmed, live, is one)
// that resolve which tenant/instance to serve purely from the request's Host header, and
// reject any Host they don't recognize as a configured domain for that instance — even when
// the request otherwise reaches the right server. This bites exactly the deployment shape
// this package is designed for: url reaching the IdP via a Kubernetes-internal Service DNS
// name (so it resolves and is dialable from inside the cluster) while the IdP's own
// configured external domain is a developer-facing "localhost:<port>" (what a human's browser
// uses via kubectl port-forward, unrelated to the cluster's internal DNS). Reproduced live:
// fetching Zitadel's JWKS via its in-cluster Service DNS name returned a real HTTP 404
// ("Instance not found. Make sure you got the domain right.") until the Host header was set
// to match Zitadel's own ExternalDomain/ExternalPort config, confirmed via a manual curl
// A/B test against a real cluster (see task-5-report.md).
func FetchJWKS(ctx context.Context, url, hostHeader string) (jwk.Set, error) {
	var opts []jwk.FetchOption
	if hostHeader != "" {
		opts = append(opts, jwk.WithHTTPClient(&http.Client{
			Timeout:   30 * time.Second,
			Transport: &hostOverrideTransport{host: hostHeader},
		}))
	}
	set, err := jwk.Fetch(ctx, url, opts...)
	if err != nil {
		return nil, fmt.Errorf("auth: fetch JWKS from %s: %w", url, err)
	}
	return set, nil
}

// hostOverrideTransport delegates to http.DefaultTransport, rewriting each outgoing request's
// Host header to a fixed value while leaving the actual dial target (req.URL.Host) untouched
// — see FetchJWKS's doc comment for why this is needed.
type hostOverrideTransport struct {
	host string
}

func (t *hostOverrideTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Host = t.host
	return http.DefaultTransport.RoundTrip(req)
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
