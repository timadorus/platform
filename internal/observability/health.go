package observability

import (
	"context"
	"net/http"
)

// Pinger is satisfied by *pgxpool.Pool. Kept as a small local interface (rather than
// importing pgxpool here) so this package stays a leaf dependency.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthzHandler is a pure liveness check: it returns 200 as soon as the process can serve
// HTTP at all, regardless of downstream dependency health. Mounted at /healthz, exempted
// from auth/schema validation (see internal/auth.Middleware's operationalPaths).
func HealthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}

// ReadyzHandler is a readiness check: it pings every dependency the binary actually needs
// (typically just the Postgres pool) and returns 503 if any is unreachable — signalling to a
// load balancer/orchestrator that this instance shouldn't receive traffic yet. Mounted at
// /readyz, exempted from auth/schema validation.
func ReadyzHandler(pingers ...Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for _, p := range pingers {
			if err := p.Ping(r.Context()); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("not ready: " + err.Error()))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	}
}
