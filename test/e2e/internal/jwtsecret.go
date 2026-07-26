package e2eutil

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"time"

	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

const (
	// JWTSecretName is the name of the Kubernetes Secret this suite creates, and the value
	// the timadorus-platform chart's jwt.hmac.existingSecret should reference.
	JWTSecretName = "jwt-hmac-secret"
	// JWTKeyID must match the timadorus-platform chart's jwt.hmac.keyID value exactly — the
	// verifier only accepts tokens whose "kid" header matches the configured key's kid (see
	// internal/auth.NewStaticSecretKeySet).
	JWTKeyID = "e2e"
	// jwtSecretKey is the key within JWTSecretName holding the raw HMAC secret, matching the
	// chart's jwt.hmac.secretKey default.
	jwtSecretKey = "JWT_HMAC_SECRET"
)

// EnsureJWTSecret generates a random, hex-encoded 32-byte HMAC secret, creates (replacing any
// existing one) the JWTSecretName Secret in Namespace holding it, and returns the secret
// string for this suite's own token-minting. A hex-encoded string (not raw binary) is used
// throughout so the exact same bytes end up both in the Kubernetes Secret (via a
// --from-literal CLI argument) and in this process's signing key — internal/auth's HMAC path
// does []byte(cfg.HMACSecret) on the literal env var string, so the two must match exactly.
func EnsureJWTSecret() (secret string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("e2eutil: generate HMAC secret: %w", err)
	}
	secret = hex.EncodeToString(raw)

	_, _ = Run(exec.Command("kubectl", "delete", "secret", JWTSecretName, "--namespace", Namespace, "--ignore-not-found"))

	_, err = Run(exec.Command("kubectl", "create", "secret", "generic", JWTSecretName,
		"--namespace", Namespace,
		fmt.Sprintf("--from-literal=%s=%s", jwtSecretKey, secret),
	))
	if err != nil {
		return "", fmt.Errorf("e2eutil: create JWT secret: %w", err)
	}
	return secret, nil
}

// MintToken signs a short-lived HS256 bearer token against secret, setting JWTKeyID as the
// token's "kid" header. This is the exact counterpart of
// internal/auth.NewStaticSecretKeySet in the platform's own code, letting this suite
// authenticate without any real identity provider.
func MintToken(secret string) (string, error) {
	key, err := jwk.Import([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("e2eutil: import HMAC key: %w", err)
	}
	if err := key.Set(jwk.KeyIDKey, JWTKeyID); err != nil {
		return "", fmt.Errorf("e2eutil: set kid: %w", err)
	}

	now := time.Now()
	token, err := jwt.NewBuilder().
		Subject(uuid.NewString()).
		IssuedAt(now).
		Expiration(now.Add(time.Hour)).
		Build()
	if err != nil {
		return "", fmt.Errorf("e2eutil: build token: %w", err)
	}

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.HS256(), key))
	if err != nil {
		return "", fmt.Errorf("e2eutil: sign token: %w", err)
	}
	return string(signed), nil
}
