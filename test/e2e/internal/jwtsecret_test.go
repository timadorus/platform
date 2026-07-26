//go:build e2e

package e2eutil

import (
	"context"
	"testing"

	"github.com/timadorus/platform/internal/auth"
)

// TestMintToken_VerifiesAgainstProductionVerifier round-trips a minted token through the
// platform's own production Verifier — the same code path command-api/query-api use — to
// prove this suite's tokens are actually accepted, not just well-formed.
func TestMintToken_VerifiesAgainstProductionVerifier(t *testing.T) {
	const secret = "test-secret-at-least-32-bytes-long!!"

	token, err := MintToken(secret)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}

	keySet, err := auth.NewStaticSecretKeySet(JWTKeyID, []byte(secret))
	if err != nil {
		t.Fatalf("NewStaticSecretKeySet: %v", err)
	}
	verifier := auth.NewVerifier(keySet, "", "")

	claims, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject == "" {
		t.Fatal("expected a non-empty subject claim")
	}
}
