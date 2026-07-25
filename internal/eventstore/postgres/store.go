// Package postgres implements eventsourcing.EventStore against a Postgres "events" table,
// with a transactional outbox row written in the same transaction as each event (see
// migrations/0001_events.sql, 0002_outbox.sql). It also owns the ambient-transaction
// plumbing (tx.go) and the UnitOfWork helper (unit_of_work.go) used by command services that
// must create more than one aggregate atomically (e.g. Character + Entity).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/eventsourcing"
)

const uniqueViolation = "23505"

// Store implements eventsourcing.EventStore.
type Store struct {
	pool     *pgxpool.Pool
	registry *eventsourcing.Registry
}

func NewStore(pool *pgxpool.Pool, registry *eventsourcing.Registry) *Store {
	return &Store{pool: pool, registry: registry}
}

// querier is satisfied by both *pgxpool.Pool and pgx.Tx.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (s *Store) Append(ctx context.Context, aggregateType string, id uuid.UUID, expectedVersion int, events []eventsourcing.Event) error {
	if len(events) == 0 {
		return nil
	}

	if tx, ok := txFromContext(ctx); ok {
		return s.appendWith(ctx, tx, aggregateType, id, expectedVersion, events)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin append tx: %w", err)
	}
	if err := s.appendWith(ctx, tx, aggregateType, id, expectedVersion, events); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit append tx: %w", err)
	}
	return nil
}

func (s *Store) appendWith(ctx context.Context, q querier, aggregateType string, id uuid.UUID, expectedVersion int, events []eventsourcing.Event) error {
	for i, event := range events {
		version := expectedVersion + i + 1

		payload, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("postgres: marshal event payload: %w", err)
		}

		var globalSeq int64
		err = q.QueryRow(ctx,
			`INSERT INTO events (aggregate_id, aggregate_type, version, event_type, payload, metadata)
			 VALUES ($1, $2, $3, $4, $5, '{}')
			 RETURNING global_seq`,
			id, aggregateType, version, event.EventType(), payload,
		).Scan(&globalSeq)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return eventsourcing.ErrConcurrencyConflict
			}
			return fmt.Errorf("postgres: insert event: %w", err)
		}

		if _, err := q.Exec(ctx,
			`INSERT INTO outbox (global_seq, aggregate_id, aggregate_type, version, event_type, payload, metadata)
			 VALUES ($1, $2, $3, $4, $5, $6, '{}')`,
			globalSeq, id, aggregateType, version, event.EventType(), payload,
		); err != nil {
			return fmt.Errorf("postgres: insert outbox row: %w", err)
		}
	}
	return nil
}

func (s *Store) Load(ctx context.Context, aggregateType string, id uuid.UUID) ([]eventsourcing.Event, int, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT version, event_type, payload FROM events
		 WHERE aggregate_id = $1 AND aggregate_type = $2
		 ORDER BY version`,
		id, aggregateType,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: query events: %w", err)
	}
	defer rows.Close()

	var events []eventsourcing.Event
	version := 0
	for rows.Next() {
		var (
			eventType string
			payload   []byte
		)
		if err := rows.Scan(&version, &eventType, &payload); err != nil {
			return nil, 0, fmt.Errorf("postgres: scan event row: %w", err)
		}
		event, err := s.registry.New(eventType)
		if err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal(payload, event); err != nil {
			return nil, 0, fmt.Errorf("postgres: unmarshal event payload for %q: %w", eventType, err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("postgres: iterate event rows: %w", err)
	}
	if len(events) == 0 {
		return nil, 0, eventsourcing.ErrAggregateNotFound
	}
	return events, version, nil
}
