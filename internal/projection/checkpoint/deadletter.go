package checkpoint

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
)

// RecordDeadLetter persists a message that a projector failed to handle maxAttempts times in
// a row, so it can be inspected/replayed manually later without blocking that projection's
// single consumer forever (see Router's poison-queue policy). Uses pool directly rather than
// an ambient tx — the failed Handle's own transaction has already been rolled back by the
// time this is called.
func RecordDeadLetter(ctx context.Context, pool *pgxpool.Pool, projectionName, messageUUID string, env bus.Envelope, handleErr error) error {
	envelopeJSON, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("checkpoint: marshal envelope for dead letter: %w", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO projection_dead_letters
		   (projection_name, message_uuid, global_seq, aggregate_id, aggregate_type, event_type, envelope, error)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		projectionName, messageUUID, env.GlobalSeq, env.AggregateID, env.AggregateType, env.EventType, envelopeJSON, handleErr.Error(),
	); err != nil {
		return fmt.Errorf("checkpoint: record dead letter: %w", err)
	}
	return nil
}
