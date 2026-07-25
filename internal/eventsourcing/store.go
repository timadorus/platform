package eventsourcing

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrConcurrencyConflict is returned by EventStore.Append when expectedVersion doesn't
// match the aggregate's current version in storage.
var ErrConcurrencyConflict = errors.New("eventsourcing: concurrency conflict")

// ErrAggregateNotFound is returned by EventStore.Load when no events exist for the given
// aggregate id.
var ErrAggregateNotFound = errors.New("eventsourcing: aggregate not found")

// EventStore is the storage-agnostic port implemented by internal/eventstore/postgres.
type EventStore interface {
	// Append persists events for the given aggregate, enforcing optimistic concurrency:
	// expectedVersion must equal the aggregate's version before these events were raised.
	Append(ctx context.Context, aggregateType string, id uuid.UUID, expectedVersion int, events []Event) error

	// Load replays all events for the given aggregate id and returns them in order along
	// with the resulting version (== len(events)).
	Load(ctx context.Context, aggregateType string, id uuid.UUID) (events []Event, version int, err error)
}
