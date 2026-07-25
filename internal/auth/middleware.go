package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

var errMissingBearerToken = errors.New("auth: missing or malformed bearer token")

// operationalPaths are mounted directly on each binary's router (see
// internal/observability.HealthzHandler/ReadyzHandler and cmd/*/main.go's /metrics
// registration) rather than declared in the OpenAPI spec — they're not part of the public
// command/query contract, so they must skip both schema validation and bearer-token auth.
var operationalPaths = map[string]bool{
	"/healthz": true,
	"/readyz":  true,
	"/metrics": true,
}

// Middleware builds the oapi-codegen request-validator middleware for spec, wired to verify
// bearer tokens declared via the spec's bearerAuth securityScheme. Mounted with
// mux.Router.Use in each binary (see plan §10) — validates both the request body/params
// against the spec (-> 400) and, for operations requiring auth, the bearer token (-> 401).
func Middleware(spec *openapi3.T, verifier *Verifier) func(http.Handler) http.Handler {
	return nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: authenticationFunc(verifier),
		},
		// The deployment topology (reverse-proxy vs. TLS-terminating-in-binary, exact
		// host/port) isn't decided yet (plan §13), so Host-header validation against the
		// spec's `servers` entry would just produce noisy false positives for now.
		DoNotValidateServers:  true,
		SilenceServersWarning: true,
		Skipper: func(r *http.Request) bool {
			return operationalPaths[r.URL.Path]
		},
	})
}

func authenticationFunc(verifier *Verifier) openapi3filter.AuthenticationFunc {
	return func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
		if input.SecuritySchemeName != "bearerAuth" {
			return nil
		}

		req := input.RequestValidationInput.Request
		token, ok := bearerToken(req.Header.Get("Authorization"))
		if !ok {
			return errMissingBearerToken
		}

		claims, err := verifier.Verify(ctx, token)
		if err != nil {
			return err
		}

		// req is the same *http.Request that continues on to the handler chain (see
		// nethttp-middleware's validateRequest, which passes r by pointer throughout), so
		// mutating it in place is how claims reach downstream handlers.
		*req = *req.WithContext(WithClaims(req.Context(), claims))
		return nil
	}
}

func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}
