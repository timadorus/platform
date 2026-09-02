package universechanges_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/query/universechanges"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../../projection/universechanges/migrations/0001_universe_changes_read_model.up.sql",
		),
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

func seedChange(t *testing.T, pool *pgxpool.Pool, globalSeq int64, universeID, aggregateID uuid.UUID, aggregateType, eventType string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO universe_changes_read_model (global_seq, universe_id, aggregate_type, aggregate_id, event_type, occurred_at)
		 VALUES ($1, $2, $3, $4, $5, now())`,
		globalSeq, universeID, aggregateType, aggregateID, eventType,
	); err != nil {
		t.Fatalf("seed change: %v", err)
	}
}

func TestRepository_Cursor_NoRows_ReturnsZero(t *testing.T) {
	pool := newTestPool(t)
	repo := universechanges.NewRepository(pool)

	cursor, err := repo.Cursor(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cursor != 0 {
		t.Fatalf("got cursor %d, want 0", cursor)
	}
}

func TestRepository_Cursor_ReturnsMaxGlobalSeq(t *testing.T) {
	pool := newTestPool(t)
	repo := universechanges.NewRepository(pool)
	universeID := uuid.New()
	seedChange(t, pool, 1, universeID, uuid.New(), "campaign", "campaign.created.v1")
	seedChange(t, pool, 5, universeID, uuid.New(), "entity", "entity.created.v1")
	seedChange(t, pool, 3, universeID, uuid.New(), "object", "object.created.v1")

	cursor, err := repo.Cursor(context.Background(), universeID)
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cursor != 5 {
		t.Fatalf("got cursor %d, want 5", cursor)
	}
}

func TestRepository_List_OrderedAndScoped(t *testing.T) {
	pool := newTestPool(t)
	repo := universechanges.NewRepository(pool)
	universeA, universeB := uuid.New(), uuid.New()
	seedChange(t, pool, 1, universeA, uuid.New(), "campaign", "campaign.created.v1")
	seedChange(t, pool, 3, universeA, uuid.New(), "entity", "entity.created.v1")
	seedChange(t, pool, 2, universeA, uuid.New(), "object", "object.created.v1")
	seedChange(t, pool, 4, universeB, uuid.New(), "campaign", "campaign.created.v1")

	changes, err := repo.List(context.Background(), universeA, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(changes) != 3 {
		t.Fatalf("got %d changes, want 3 (must not include universe B's row)", len(changes))
	}
	if changes[0].GlobalSeq != 1 || changes[1].GlobalSeq != 2 || changes[2].GlobalSeq != 3 {
		t.Fatalf("got global_seq order %d,%d,%d, want 1,2,3", changes[0].GlobalSeq, changes[1].GlobalSeq, changes[2].GlobalSeq)
	}
}

func TestRepository_List_SinceExcludesUpToAndIncludingCursor(t *testing.T) {
	pool := newTestPool(t)
	repo := universechanges.NewRepository(pool)
	universeID := uuid.New()
	seedChange(t, pool, 1, universeID, uuid.New(), "campaign", "campaign.created.v1")
	seedChange(t, pool, 2, universeID, uuid.New(), "entity", "entity.created.v1")

	changes, err := repo.List(context.Background(), universeID, 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(changes) != 1 || changes[0].GlobalSeq != 2 {
		t.Fatalf("got %v, want exactly global_seq 2 (since=1 is exclusive)", changes)
	}
}

func TestRepository_List_CapsAt20(t *testing.T) {
	pool := newTestPool(t)
	repo := universechanges.NewRepository(pool)
	universeID := uuid.New()
	for i := int64(1); i <= 25; i++ {
		seedChange(t, pool, i, universeID, uuid.New(), "campaign", "campaign.created.v1")
	}

	changes, err := repo.List(context.Background(), universeID, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(changes) != 20 {
		t.Fatalf("got %d changes, want 20 (capped)", len(changes))
	}
	if changes[0].GlobalSeq != 1 || changes[19].GlobalSeq != 20 {
		t.Fatalf("got first/last global_seq %d/%d, want 1/20", changes[0].GlobalSeq, changes[19].GlobalSeq)
	}
}
