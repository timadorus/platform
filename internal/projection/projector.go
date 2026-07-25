// Package projection is the generic projection framework: the Projector contract every
// read-model projector implements, and the Router that wires each one to its own durable
// NATS JetStream consumer with idempotent, checkpointed processing (see docs/adr and plan
// §7). Adding a new projection means writing a new Projector and adding one line to
// cmd/projector/main.go's registration list — this package itself never changes.
package projection

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
)

// Projector handles one aggregate type's events, writing to its own read-model tables. It
// must import only that aggregate's events sub-package (e.g. domain/universe/events), never
// the invariant-bearing domain package itself — see plan's read/write import-graph rule.
type Projector interface {
	// Name is both the durable JetStream consumer name and the checkpoint table key. Must
	// be stable across restarts/deploys.
	Name() string

	// Subjects lists the bus subjects this projector consumes (see internal/bus.Subject).
	// Almost always a single subject, one per aggregate type.
	Subjects() []string

	// Handle applies a single event to this projector's read-model tables, inside tx (the
	// same transaction the Router uses for the checkpoint update — see Router.handle).
	Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error
}
