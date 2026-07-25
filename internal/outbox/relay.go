// Package outbox implements the transactional-outbox relay: a background loop, embedded in
// command-api, that tails the outbox table and publishes unpublished rows to NATS JetStream
// in strict global_seq order (see docs/adr/0002 for the ordering rationale).
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
)

const (
	defaultPollInterval = 200 * time.Millisecond
	defaultBatchSize    = 100
)

// LeaderKey is the pg_advisory_lock key shared by every command-api replica contending to
// be the active outbox relay.
const LeaderKey int64 = 0x7469_6d61 // "tima" - arbitrary, stable across restarts/replicas

type Relay struct {
	pool      *pgxpool.Pool
	publisher message.Publisher
	leader    *Leader
	logger    *slog.Logger

	pollInterval time.Duration
	batchSize    int
}

func NewRelay(pool *pgxpool.Pool, publisher message.Publisher, logger *slog.Logger) *Relay {
	return &Relay{
		pool:         pool,
		publisher:    publisher,
		leader:       NewLeader(pool, LeaderKey),
		logger:       logger,
		pollInterval: defaultPollInterval,
		batchSize:    defaultBatchSize,
	}
}

// Run polls until ctx is cancelled. Intended to be started in its own goroutine by
// cmd/command-api/main.go.
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.leader.Release(context.Background())
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *Relay) tick(ctx context.Context) {
	acquired, err := r.leader.TryAcquire(ctx)
	if err != nil {
		r.logger.Error("outbox: leader election failed", "error", err)
		return
	}
	if !acquired {
		return // another replica is the active relay
	}
	if err := r.publishBatch(ctx); err != nil {
		r.logger.Error("outbox: publish batch failed", "error", err)
	}
}

type outboxRow struct {
	id            int64
	globalSeq     int64
	aggregateID   uuid.UUID
	aggregateType string
	version       int
	eventType     string
	payload       json.RawMessage
	metadata      json.RawMessage
}

func (r *Relay) publishBatch(ctx context.Context) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("outbox: begin batch tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	rows, err := tx.Query(ctx,
		`SELECT id, global_seq, aggregate_id, aggregate_type, version, event_type, payload, metadata
		 FROM outbox WHERE published_at IS NULL
		 ORDER BY id
		 FOR UPDATE SKIP LOCKED
		 LIMIT $1`,
		r.batchSize,
	)
	if err != nil {
		return fmt.Errorf("outbox: query batch: %w", err)
	}
	var batch []outboxRow
	for rows.Next() {
		var row outboxRow
		if err := rows.Scan(&row.id, &row.globalSeq, &row.aggregateID, &row.aggregateType,
			&row.version, &row.eventType, &row.payload, &row.metadata); err != nil {
			rows.Close()
			return fmt.Errorf("outbox: scan row: %w", err)
		}
		batch = append(batch, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("outbox: iterate batch: %w", err)
	}

	for _, row := range batch {
		envelope := bus.Envelope{
			GlobalSeq:     row.globalSeq,
			AggregateID:   row.aggregateID,
			AggregateType: row.aggregateType,
			Version:       row.version,
			EventType:     row.eventType,
			Payload:       row.payload,
			Metadata:      row.metadata,
		}
		body, err := json.Marshal(envelope)
		if err != nil {
			return fmt.Errorf("outbox: marshal envelope for outbox row %d: %w", row.id, err)
		}

		msg := message.NewMessage(watermill.NewUUID(), body)
		if err := r.publisher.Publish(bus.Subject(row.aggregateType), msg); err != nil {
			// Not committing this row (or any after it in the batch) is deliberate: on
			// retry the whole unpublished batch is re-attempted. Rows already published
			// earlier in this same batch may be re-published too, which is safe because
			// projections deduplicate via their checkpoint (plan §7) and NATS delivery is
			// at-least-once regardless.
			return fmt.Errorf("outbox: publish row %d: %w", row.id, err)
		}

		if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = $1`, row.id); err != nil {
			return fmt.Errorf("outbox: mark row %d published: %w", row.id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("outbox: commit batch: %w", err)
	}
	return nil
}
