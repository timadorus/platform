// Package projection is the generic projection framework: the Projector contract every
// read-model projector implements, and the Router that wires each one to its own durable
// NATS JetStream consumer with idempotent, checkpointed processing (see docs/adr and plan
// §7). Adding a new read-model projection means writing a new Projector and adding one line
// to cmd/projector/main.go's registration list — this package itself never changes. That
// registration rule is specific to read-model projectors run from cmd/projector; see the
// Projector interface's doc comment for the one deliberate exception.
package projection

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
)

// Projector handles one aggregate type's events, writing to its own read-model tables. A
// read-model projector registered in cmd/projector must import only that aggregate's events
// sub-package (e.g. domain/universe/events), never the invariant-bearing domain package
// itself — see plan's read/write import-graph rule.
//
// internal/engine/timadorus.Processor is a deliberate exception: it implements this same
// interface but is not a read-model projector — it's a process-manager-shaped consumer with
// legitimate write-side access (it loads and saves a Character aggregate), and it registers
// into cmd/timadorus-engine/main.go, not cmd/projector/main.go. It runs in its own binary for
// exactly that reason, so it does not violate the import rule above; that rule scopes only to
// projectors living in cmd/projector.
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
