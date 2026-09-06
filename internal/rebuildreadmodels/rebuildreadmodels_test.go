package rebuildreadmodels_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/rebuildreadmodels"
)

// newTestPool starts a fresh Postgres testcontainer with the events and projection_checkpoints
// migrations this package's tests need — mirrors every other package in this codebase's own
// per-package newTestPool convention (see e.g. internal/engine/timadorus/testutil_test.go).
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../eventstore/postgres/migrations/0001_events.up.sql",
			"../projection/checkpoint/migrations/0001_projection_checkpoints.up.sql",
		),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

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

func TestResetCheckpoint_SetsToZero(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	if err := rebuildreadmodels.SetCheckpoint(ctx, pool, "test-projector", 42); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	if err := rebuildreadmodels.ResetCheckpoint(ctx, pool, "test-projector"); err != nil {
		t.Fatalf("ResetCheckpoint: %v", err)
	}
	got, err := rebuildreadmodels.CurrentCheckpoint(ctx, pool, "test-projector")
	if err != nil {
		t.Fatalf("CurrentCheckpoint: %v", err)
	}
	if got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

func TestMaxGlobalSeq_EmptyEventsTable_ReturnsZero(t *testing.T) {
	pool := newTestPool(t)
	got, err := rebuildreadmodels.MaxGlobalSeq(context.Background(), pool)
	if err != nil {
		t.Fatalf("MaxGlobalSeq: %v", err)
	}
	if got != 0 {
		t.Fatalf("got %d, want 0 on an empty events table", got)
	}
}

func TestMaxGlobalSeq_ReturnsCurrentMax(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO events (aggregate_id, aggregate_type, version, event_type, payload)
			 VALUES (gen_random_uuid(), 'test', $1, 'test.event.v1', '{}')`, i+1,
		); err != nil {
			t.Fatalf("insert event %d: %v", i, err)
		}
	}

	got, err := rebuildreadmodels.MaxGlobalSeq(ctx, pool)
	if err != nil {
		t.Fatalf("MaxGlobalSeq: %v", err)
	}
	if got != 3 {
		t.Fatalf("got %d, want 3 (one row per insert, BIGSERIAL starts at 1)", got)
	}
}

func TestWaitForCatchUp_AlreadyCaughtUp_ReturnsImmediately(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	for _, name := range []string{"a", "b"} {
		if err := rebuildreadmodels.SetCheckpoint(ctx, pool, name, 10); err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
	}

	start := time.Now()
	// A long interval — if WaitForCatchUp checked-then-waited instead of the reverse, this test
	// would take at least that long; it must not, since both names are already at target.
	err := rebuildreadmodels.WaitForCatchUp(ctx, pool, []rebuildreadmodels.ProjectorTarget{
		{Name: "a", Target: 10},
		{Name: "b", Target: 10},
	}, time.Minute, nil)
	if err != nil {
		t.Fatalf("WaitForCatchUp: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %s, want near-instant (already caught up, must check before waiting)", elapsed)
	}
}

func TestWaitForCatchUp_PollsUntilExternallyBumped(t *testing.T) {
	pool := newTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := rebuildreadmodels.SetCheckpoint(ctx, pool, "slow", 0); err != nil {
		t.Fatalf("seed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- rebuildreadmodels.WaitForCatchUp(ctx, pool,
			[]rebuildreadmodels.ProjectorTarget{{Name: "slow", Target: 5}}, 50*time.Millisecond, nil)
	}()

	time.Sleep(200 * time.Millisecond)
	if err := rebuildreadmodels.SetCheckpoint(ctx, pool, "slow", 5); err != nil {
		t.Fatalf("bump: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForCatchUp: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitForCatchUp did not return within 5s of being bumped to target")
	}
}
