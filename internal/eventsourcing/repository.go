package eventsourcing

import (
	"context"

	"github.com/google/uuid"
)

// Repository provides Load/Save for a single aggregate type with optimistic concurrency,
// eliminating duplicated replay/append plumbing across every aggregate type. It is the one
// generic in this codebase, and it earns its keep by being reused unchanged by all six
// aggregate types.
type Repository[T Aggregate] struct {
	store         EventStore
	aggregateType string
	newBlank      func() T
}

// NewRepository constructs a Repository for aggregateType. newBlank must return a fresh,
// zero-value concrete aggregate (e.g. func() *universe.Universe { return &universe.Universe{} }).
func NewRepository[T Aggregate](store EventStore, aggregateType string, newBlank func() T) *Repository[T] {
	return &Repository[T]{store: store, aggregateType: aggregateType, newBlank: newBlank}
}

// Load replays an aggregate's full event stream and returns it hydrated to its current
// version. Returns ErrAggregateNotFound if no events exist for id.
func (r *Repository[T]) Load(ctx context.Context, id uuid.UUID) (T, error) {
	var zero T
	events, version, err := r.store.Load(ctx, r.aggregateType, id)
	if err != nil {
		return zero, err
	}
	agg := r.newBlank()
	agg.SetID(id)
	for _, e := range events {
		agg.Apply(e)
	}
	agg.SetVersion(version)
	return agg, nil
}

// Save appends any pending events raised on agg since it was loaded (or created), using
// the aggregate's pre-raise version for the optimistic-concurrency check. A no-op if there
// are no pending events.
func (r *Repository[T]) Save(ctx context.Context, agg T) error {
	pending := agg.Pending()
	if len(pending) == 0 {
		return nil
	}
	expectedVersion := agg.Version() - len(pending)
	if err := r.store.Append(ctx, r.aggregateType, agg.AggregateID(), expectedVersion, pending); err != nil {
		return err
	}
	agg.ClearPending()
	return nil
}
