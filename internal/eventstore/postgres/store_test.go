package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/domain/universe"
	"github.com/timadorus/platform/internal/domain/universe/events"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithInitScripts("migrations/0001_events.up.sql", "migrations/0002_outbox.up.sql"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestStore_AppendAndLoad_Roundtrip(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	id := uuid.New()
	creator := uuid.New()
	created := &events.UniverseCreated{ID: id, Name: "Krynn", CreatorUserIDs: []uuid.UUID{creator}, OccurredAt: time.Now().UTC()}
	renamed := &events.UniverseRenamed{Name: "Dragonlance", OccurredAt: time.Now().UTC()}

	if err := store.Append(ctx, universe.AggregateType, id, 0, []eventsourcing.Event{created}); err != nil {
		t.Fatalf("append created: %v", err)
	}
	if err := store.Append(ctx, universe.AggregateType, id, 1, []eventsourcing.Event{renamed}); err != nil {
		t.Fatalf("append renamed: %v", err)
	}

	loaded, version, err := store.Load(ctx, universe.AggregateType, id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if version != 2 {
		t.Fatalf("got version %d, want 2", version)
	}
	if len(loaded) != 2 {
		t.Fatalf("got %d events, want 2", len(loaded))
	}
	gotCreated, ok := loaded[0].(*events.UniverseCreated)
	if !ok || gotCreated.Name != "Krynn" {
		t.Fatalf("unexpected first event: %#v", loaded[0])
	}
	gotRenamed, ok := loaded[1].(*events.UniverseRenamed)
	if !ok || gotRenamed.Name != "Dragonlance" {
		t.Fatalf("unexpected second event: %#v", loaded[1])
	}

	// One outbox row per appended event, all in the same DB the events landed in — proves
	// the transactional-outbox write happened alongside the event write.
	var outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE aggregate_id = $1`, id).Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if outboxCount != 2 {
		t.Fatalf("got %d outbox rows, want 2", outboxCount)
	}
}

func TestStore_Append_ConcurrencyConflict(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	id := uuid.New()
	created := &events.UniverseCreated{ID: id, Name: "Krynn", CreatorUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC()}
	if err := store.Append(ctx, universe.AggregateType, id, 0, []eventsourcing.Event{created}); err != nil {
		t.Fatalf("append created: %v", err)
	}

	// Re-appending at the same expectedVersion simulates two command handlers racing on a
	// stale read of the aggregate.
	conflicting := &events.UniverseRenamed{Name: "Conflict", OccurredAt: time.Now().UTC()}
	err := store.Append(ctx, universe.AggregateType, id, 0, []eventsourcing.Event{conflicting})
	if err != eventsourcing.ErrConcurrencyConflict {
		t.Fatalf("got %v, want ErrConcurrencyConflict", err)
	}

	// And the conflicting event must not have landed in the outbox either.
	var outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE aggregate_id = $1`, id).Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("got %d outbox rows, want 1 (conflicting append must not leave a partial row)", outboxCount)
	}
}

func TestStore_Load_NotFound(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	_, _, err := store.Load(ctx, universe.AggregateType, uuid.New())
	if err != eventsourcing.ErrAggregateNotFound {
		t.Fatalf("got %v, want ErrAggregateNotFound", err)
	}
}

func TestUnitOfWork_AtomicAcrossAggregates(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	idA := uuid.New()
	idB := uuid.New()
	eventA := &events.UniverseCreated{ID: idA, Name: "A", CreatorUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC()}
	eventB := &events.UniverseCreated{ID: idB, Name: "B", CreatorUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC()}

	uow, txCtx, err := postgres.NewUnitOfWork(ctx, pool)
	if err != nil {
		t.Fatalf("new unit of work: %v", err)
	}
	if err := store.Append(txCtx, universe.AggregateType, idA, 0, []eventsourcing.Event{eventA}); err != nil {
		t.Fatalf("append A: %v", err)
	}
	if err := store.Append(txCtx, universe.AggregateType, idB, 0, []eventsourcing.Event{eventB}); err != nil {
		t.Fatalf("append B: %v", err)
	}
	if err := uow.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Neither aggregate should exist: the whole unit of work was rolled back.
	if _, _, err := store.Load(ctx, universe.AggregateType, idA); err != eventsourcing.ErrAggregateNotFound {
		t.Fatalf("aggregate A: got %v, want ErrAggregateNotFound after rollback", err)
	}
	if _, _, err := store.Load(ctx, universe.AggregateType, idB); err != eventsourcing.ErrAggregateNotFound {
		t.Fatalf("aggregate B: got %v, want ErrAggregateNotFound after rollback", err)
	}

	// Now the successful path: both commit together.
	uow2, txCtx2, err := postgres.NewUnitOfWork(ctx, pool)
	if err != nil {
		t.Fatalf("new unit of work: %v", err)
	}
	if err := store.Append(txCtx2, universe.AggregateType, idA, 0, []eventsourcing.Event{eventA}); err != nil {
		t.Fatalf("append A: %v", err)
	}
	if err := store.Append(txCtx2, universe.AggregateType, idB, 0, []eventsourcing.Event{eventB}); err != nil {
		t.Fatalf("append B: %v", err)
	}
	if err := uow2.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if _, _, err := store.Load(ctx, universe.AggregateType, idA); err != nil {
		t.Fatalf("load A after commit: %v", err)
	}
	if _, _, err := store.Load(ctx, universe.AggregateType, idB); err != nil {
		t.Fatalf("load B after commit: %v", err)
	}
}
