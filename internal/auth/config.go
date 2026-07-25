package auth

import (
	"context"
	"log/slog"
)

// Config is the subset of internal/config.JWT needed to build a Verifier. Declared here
// (rather than importing internal/config directly) so internal/auth stays independent of
// where its configuration happens to live; internal/config.JWT satisfies this by having the
// same field names/types.
type Config struct {
	JWKSURL    string
	HMACSecret string
	HMACKeyID  string
	Issuer     string
	Audience   string
}

// insecureDevSecret is used only when neither JWKSURL nor HMACSecret is configured, so
// `go run ./cmd/command-api` (or query-api) works out of the box locally. Never valid in
// production — NewVerifierFromConfig logs loudly when it's used.
const insecureDevSecret = "insecure-dev-secret-change-me"

// NewVerifierFromConfig builds a Verifier from JWKS or a static HMAC secret, shared by both
// command-api and query-api (plan §10: both APIs validate the same bearer tokens). Falls
// back to a well-known insecure dev secret if nothing is configured.
func NewVerifierFromConfig(ctx context.Context, cfg Config, logger *slog.Logger, service string) (*Verifier, error) {
	switch {
	case cfg.JWKSURL != "":
		keySet, err := FetchJWKS(ctx, cfg.JWKSURL)
		if err != nil {
			return nil, err
		}
		return NewVerifier(keySet, cfg.Issuer, cfg.Audience), nil
	case cfg.HMACSecret != "":
		keySet, err := NewStaticSecretKeySet(cfg.HMACKeyID, []byte(cfg.HMACSecret))
		if err != nil {
			return nil, err
		}
		return NewVerifier(keySet, cfg.Issuer, cfg.Audience), nil
	default:
		logger.Warn(service + ": JWT_JWKS_URL/JWT_HMAC_SECRET not set, using insecure dev default — do not run this in production")
		keySet, err := NewStaticSecretKeySet(cfg.HMACKeyID, []byte(insecureDevSecret))
		if err != nil {
			return nil, err
		}
		return NewVerifier(keySet, cfg.Issuer, cfg.Audience), nil
	}
}
