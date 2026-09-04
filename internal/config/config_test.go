package config_test

import (
	"strings"
	"testing"

	"github.com/timadorus/platform/internal/config"
)

func TestLoadTimadorusEngine_PoolMaxConnsDefault(t *testing.T) {
	cfg, err := config.LoadTimadorusEngine()
	if err != nil {
		t.Fatalf("got error %v, want nil (variable unset must not be an error)", err)
	}
	if cfg.PoolMaxConns != 8 {
		t.Fatalf("got PoolMaxConns %d, want default 8", cfg.PoolMaxConns)
	}
}

func TestLoadTimadorusEngine_PoolMaxConnsOverride(t *testing.T) {
	t.Setenv("TIMADORUS_ENGINE_POOL_MAX_CONNS", "16")
	cfg, err := config.LoadTimadorusEngine()
	if err != nil {
		t.Fatalf("got error %v, want nil for a valid positive override", err)
	}
	if cfg.PoolMaxConns != 16 {
		t.Fatalf("got PoolMaxConns %d, want overridden 16", cfg.PoolMaxConns)
	}
}

func TestLoadTimadorusEngine_PoolMaxConnsUnparseable_ReturnsError(t *testing.T) {
	t.Setenv("TIMADORUS_ENGINE_POOL_MAX_CONNS", "not-a-number")
	_, err := config.LoadTimadorusEngine()
	if err == nil {
		t.Fatal("got nil error, want an error naming the invalid value instead of silently defaulting")
	}
	if !strings.Contains(err.Error(), "TIMADORUS_ENGINE_POOL_MAX_CONNS") {
		t.Fatalf("error %q does not name the offending environment variable", err.Error())
	}
}

func TestLoadTimadorusEngine_PoolMaxConnsZero_ReturnsError(t *testing.T) {
	t.Setenv("TIMADORUS_ENGINE_POOL_MAX_CONNS", "0")
	_, err := config.LoadTimadorusEngine()
	if err == nil {
		t.Fatal("got nil error, want an error — 0 is not a usable pool size and must not silently fall back")
	}
	if !strings.Contains(err.Error(), "TIMADORUS_ENGINE_POOL_MAX_CONNS") {
		t.Fatalf("error %q does not name the offending environment variable", err.Error())
	}
}

func TestLoadTimadorusEngine_PoolMaxConnsNegative_ReturnsError(t *testing.T) {
	t.Setenv("TIMADORUS_ENGINE_POOL_MAX_CONNS", "-1")
	_, err := config.LoadTimadorusEngine()
	if err == nil {
		t.Fatal("got nil error, want an error for a negative pool size")
	}
	if !strings.Contains(err.Error(), "TIMADORUS_ENGINE_POOL_MAX_CONNS") {
		t.Fatalf("error %q does not name the offending environment variable", err.Error())
	}
}

// TestLoadTimadorusEngine_PoolMaxConnsZero_DiffersFromUnset is the direct regression test for the
// distinction that matters here: leaving TIMADORUS_ENGINE_POOL_MAX_CONNS unset and explicitly
// setting it to "0" must NOT be treated the same way. Unset silently defaults (there was never an
// opinion to validate); "0" is an explicit, invalid opinion and must fail loudly. Before this
// fix, both cases fell back to the same default 8 with no error, making them indistinguishable to
// a caller — this test fails on that old behavior and passes only once the two are told apart.
func TestLoadTimadorusEngine_PoolMaxConnsZero_DiffersFromUnset(t *testing.T) {
	unsetCfg, unsetErr := config.LoadTimadorusEngine()
	if unsetErr != nil {
		t.Fatalf("unset: got error %v, want nil", unsetErr)
	}
	if unsetCfg.PoolMaxConns != 8 {
		t.Fatalf("unset: got PoolMaxConns %d, want default 8", unsetCfg.PoolMaxConns)
	}

	t.Setenv("TIMADORUS_ENGINE_POOL_MAX_CONNS", "0")
	_, zeroErr := config.LoadTimadorusEngine()
	if zeroErr == nil {
		t.Fatal("set to \"0\": got nil error, want an error — must be told apart from leaving the variable unset")
	}
}
