package tables

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
)

// Hook is invoked when some future event handler decides a table row is relevant to an incoming
// event — table rows have no event-sourced lifecycle of their own (see this package's own doc
// comment), so nothing in this codebase yet decides that; this type exists so that future
// decision can attach behavior to a specific row once it does. Its signature deliberately
// matches projection.Projector.Handle's exactly: a hook can do anything a full event processor
// can (load/save aggregates within the same transaction), just scoped to reacting on behalf of
// one row instead of a whole event type.
type Hook func(ctx context.Context, tx pgx.Tx, env bus.Envelope) error
