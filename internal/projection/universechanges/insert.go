// Package universechanges projects Universe/Campaign/Entity/Object/Character events into
// universe_changes_read_model — a generic, per-Universe change feed a client can poll to detect
// a change made elsewhere (another tab, another user, the CLI) without already knowing the
// specific aggregate to re-check. Deliberately five independent, single-subject projectors
// (Universe/Campaign/Entity/Object/Character) rather than one projector on five subjects:
// internal/projection/router.go runs one goroutine per subject but keys its checkpoint by
// Name() alone, so a single multi-subject projector could have a higher-global_seq event from
// one subject commit the checkpoint before a lower-global_seq event from a different subject is
// processed, silently skipping it. Five single-subject projectors sidesteps this entirely, each
// getting its own checkpoint like every other projector in this codebase already does.
package universechanges

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
)

// insertChange is the one piece of logic all five projectors in this package share — pure SQL,
// no business logic, no per-event-type filtering (every event on a covered aggregate type
// produces exactly one row, unconditionally — see the design spec for why). Not shared via a
// generic container; a plain function is the right amount of sharing here.
func insertChange(ctx context.Context, tx pgx.Tx, universeID uuid.UUID, env bus.Envelope) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO universe_changes_read_model (global_seq, universe_id, aggregate_type, aggregate_id, event_type, occurred_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (global_seq) DO NOTHING`,
		env.GlobalSeq, universeID, env.AggregateType, env.AggregateID, env.EventType, env.CreatedAt,
	); err != nil {
		return fmt.Errorf("universechanges: insert change for %s %s: %w", env.AggregateType, env.AggregateID, err)
	}
	return nil
}
