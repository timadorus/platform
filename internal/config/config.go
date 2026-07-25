// Package config provides simple env-var-based configuration for each binary. There is no
// framework here deliberately — three small binaries with a handful of settings each don't
// need one.
package config

import "os"

type JWT struct {
	// JWKSURL, if set, fetches the verification key set from an identity provider. Takes
	// precedence over HMACSecret.
	JWKSURL string
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
	DatabaseURL string
	NATSURL     string
}

func LoadProjector() Projector {
	return Projector{
		DatabaseURL: getEnv("DATABASE_URL", "postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable"),
		NATSURL:     getEnv("NATS_URL", "nats://localhost:4222"),
	}
}

func loadJWT() JWT {
	return JWT{
		JWKSURL:    os.Getenv("JWT_JWKS_URL"),
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
