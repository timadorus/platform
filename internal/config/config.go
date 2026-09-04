// Package config provides simple env-var-based configuration for each binary. There is no
// framework here deliberately — three small binaries with a handful of settings each don't
// need one.
package config

import (
	"fmt"
	"os"
	"strconv"
)

type JWT struct {
	// JWKSURL, if set, fetches the verification key set from an identity provider. Takes
	// precedence over HMACSecret.
	JWKSURL string
	// JWKSHost, if set, overrides the Host header sent when fetching JWKSURL — see
	// internal/auth.FetchJWKS's doc comment for why a multi-tenant identity provider reached
	// via a network address that differs from its own configured external domain (e.g. a
	// Kubernetes-internal Service DNS name) needs this.
	JWKSHost string
	// HMACSecret, if set (and JWKSURL is not), builds a single-key static HS256 key set —
	// intended for local development/testing only, never a real identity provider.
	HMACSecret string
	// HMACKeyID must match the "kid" header on tokens signed with HMACSecret.
	HMACKeyID string
	Issuer    string
	Audience  string
}

type CommandAPI struct {
	HTTPAddr    string
	DatabaseURL string
	NATSURL     string
	JWT         JWT
}

func LoadCommandAPI() CommandAPI {
	return CommandAPI{
		HTTPAddr:    getEnv("COMMAND_API_ADDR", ":8081"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable"),
		NATSURL:     getEnv("NATS_URL", "nats://localhost:4222"),
		JWT:         loadJWT(),
	}
}

type QueryAPI struct {
	HTTPAddr    string
	DatabaseURL string
	JWT         JWT
}

func LoadQueryAPI() QueryAPI {
	return QueryAPI{
		HTTPAddr:    getEnv("QUERY_API_ADDR", ":8082"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable"),
		JWT:         loadJWT(),
	}
}

type Projector struct {
	// HTTPAddr serves /healthz, /readyz, /metrics only — the projector has no public API,
	// so unlike command-api/query-api this port carries no OpenAPI-defined routes and needs
	// no auth middleware.
	HTTPAddr    string
	DatabaseURL string
	NATSURL     string
}

func LoadProjector() Projector {
	return Projector{
		HTTPAddr:    getEnv("PROJECTOR_ADDR", ":8083"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable"),
		NATSURL:     getEnv("NATS_URL", "nats://localhost:4222"),
	}
}

type TimadorusEngine struct {
	// HTTPAddr serves /healthz, /readyz, /metrics only — same shape as Projector's, see that
	// type's doc comment.
	HTTPAddr    string
	DatabaseURL string
	NATSURL     string
	// PoolMaxConns caps the shared Postgres connection pool used by both engine processors (see
	// cmd/timadorus-engine/main.go's "Connection budget" comment for the reasoning behind the
	// default of 8). Configurable via TIMADORUS_ENGINE_POOL_MAX_CONNS so a 3rd processor sharing
	// this binary, or any other change to the per-Handle connection cost, can be given headroom
	// without a code change or redeploy of a new binary. Leaving the variable unset defaults to
	// 8; explicitly setting it to something invalid (unparseable, zero, or negative) fails
	// LoadTimadorusEngine with a named error instead of silently substituting the default — see
	// parsePoolMaxConns.
	PoolMaxConns int32
}

// LoadTimadorusEngine returns an error only when TIMADORUS_ENGINE_POOL_MAX_CONNS is explicitly
// set to something invalid (see parsePoolMaxConns) — every other field is best-effort, matching
// this package's other Load* functions. Leaving the variable unset is not an error.
func LoadTimadorusEngine() (TimadorusEngine, error) {
	poolMaxConns, err := parsePoolMaxConns("TIMADORUS_ENGINE_POOL_MAX_CONNS", 8)
	if err != nil {
		return TimadorusEngine{}, err
	}
	return TimadorusEngine{
		HTTPAddr:     getEnv("TIMADORUS_ENGINE_ADDR", ":8084"),
		DatabaseURL:  getEnv("DATABASE_URL", "postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable"),
		NATSURL:      getEnv("NATS_URL", "nats://localhost:4222"),
		PoolMaxConns: poolMaxConns,
	}, nil
}

func loadJWT() JWT {
	return JWT{
		JWKSURL:    os.Getenv("JWT_JWKS_URL"),
		JWKSHost:   os.Getenv("JWT_JWKS_HOST"),
		HMACSecret: os.Getenv("JWT_HMAC_SECRET"),
		HMACKeyID:  getEnv("JWT_HMAC_KEY_ID", "dev"),
		Issuer:     os.Getenv("JWT_ISSUER"),
		Audience:   os.Getenv("JWT_AUDIENCE"),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parsePoolMaxConns resolves a pool-size env var. Unset (or empty) is not an error — it means the
// operator didn't opt into a value at all, so def applies silently, matching every other Load*
// function's env-with-default convention. But once the operator DOES set it, the value has to be
// usable: an unparseable or non-positive value would otherwise reach pgxpool.NewWithConfig and
// fail fatally with an opaque "MaxSize must be >= 1" that never names the environment variable at
// fault. Erroring here instead gives a startup failure that says exactly which variable, what
// value was seen, and why — surfaced by the caller the same way every other fatal startup error
// in this binary already is (run()'s error return -> main()'s os.Exit(1)).
func parsePoolMaxConns(key string, def int32) (int32, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a valid integer", key, v)
	}
	if n < 1 {
		return 0, fmt.Errorf("%s=%q must be a positive integer, got %d", key, v, n)
	}
	return int32(n), nil
}
