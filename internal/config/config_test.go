package config_test

import (
	"testing"

	"github.com/timadorus/platform/internal/config"
)

func TestLoadTimadorusEngine_PoolMaxConnsDefault(t *testing.T) {
	cfg := config.LoadTimadorusEngine()
	if cfg.PoolMaxConns != 8 {
		t.Fatalf("got PoolMaxConns %d, want default 8", cfg.PoolMaxConns)
	}
}

func TestLoadTimadorusEngine_PoolMaxConnsOverride(t *testing.T) {
	t.Setenv("TIMADORUS_ENGINE_POOL_MAX_CONNS", "16")
	cfg := config.LoadTimadorusEngine()
	if cfg.PoolMaxConns != 16 {
		t.Fatalf("got PoolMaxConns %d, want overridden 16", cfg.PoolMaxConns)
	}
}

func TestLoadTimadorusEngine_PoolMaxConnsInvalid_FallsBackToDefault(t *testing.T) {
	t.Setenv("TIMADORUS_ENGINE_POOL_MAX_CONNS", "not-a-number")
	cfg := config.LoadTimadorusEngine()
	if cfg.PoolMaxConns != 8 {
		t.Fatalf("got PoolMaxConns %d, want default 8 on invalid input", cfg.PoolMaxConns)
	}
}
