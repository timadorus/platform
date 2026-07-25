// Package observability provides the cross-cutting concerns every binary needs: correlation
// ID propagation (HTTP request -> event metadata -> outbox -> NATS message -> projector
// logs), structured request logging, and Prometheus metrics (metrics.go).
package observability

import "context"

type correlationIDKey struct{}

// WithCorrelationID stashes id in ctx. HTTP middleware (Middleware, in this package) sets
// this once per request; internal/eventstore/postgres.Store.Append reads it back to stamp
// every event's metadata column, and the outbox relay copies that metadata verbatim into
// the NATS envelope (internal/bus.Envelope.Metadata) — so a single id threads from the
// original HTTP request all the way to projector logs.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey{}, id)
}

// CorrelationID returns the correlation id stashed in ctx, or "" if none was set (e.g. a
// background job not triggered by an HTTP request).
func CorrelationID(ctx context.Context) string {
	id, _ := ctx.Value(correlationIDKey{}).(string)
	return id
}
