package rulesettables_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/query/rulesettables"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../../projection/rulesettables/migrations/0001_ruleset_tables_read_model.up.sql",
			"../../projection/rulesettables/migrations/0002_content_hash_versioning.up.sql",
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

func seedRow(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID, tableName, rowKey, contentHash, data string) {
	t.Helper()
	seedRowAt(t, pool, rulesetID, tableName, rowKey, contentHash, data, time.Now())
}

func seedRowAt(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID, tableName, rowKey, contentHash, data string, updatedAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO ruleset_tables_read_model (ruleset_id, table_name, row_key, content_hash, data, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		rulesetID, tableName, rowKey, contentHash, data, updatedAt,
	); err != nil {
		t.Fatalf("seed row: %v", err)
	}
}

func TestRepository_List_ReturnsOrderedRows(t *testing.T) {
	pool := newTestPool(t)
	repo := rulesettables.NewRepository(pool)
	rulesetID := uuid.New()

	seedRow(t, pool, rulesetID, "traits", "strong", "h1", `{"displayName":"Strong"}`)
	seedRow(t, pool, rulesetID, "traits", "agile", "h1", `{"displayName":"Agile"}`)

	rows, err := repo.List(context.Background(), rulesetID, "traits")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Key != "agile" || rows[1].Key != "strong" {
		t.Fatalf("got keys [%s %s], want [agile strong] (ordered by row_key)", rows[0].Key, rows[1].Key)
	}
}

func TestRepository_List_ScopedToRulesetAndTableName(t *testing.T) {
	pool := newTestPool(t)
	repo := rulesettables.NewRepository(pool)
	rulesetA := uuid.New()
	rulesetB := uuid.New()

	seedRow(t, pool, rulesetA, "traits", "strong", "h1", `{"displayName":"Strong"}`)
	seedRow(t, pool, rulesetB, "traits", "strong", "h1", `{"displayName":"Different Strong"}`)
	seedRow(t, pool, rulesetA, "skills", "strong", "h1", `{"displayName":"A Skill Named Strong"}`)

	rows, err := repo.List(context.Background(), rulesetA, "traits")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (must not include ruleset B's or the \"skills\" table's row)", len(rows))
	}
}

func TestRepository_Get_Found(t *testing.T) {
	pool := newTestPool(t)
	repo := rulesettables.NewRepository(pool)
	rulesetID := uuid.New()
	seedRow(t, pool, rulesetID, "traits", "strong", "h1", `{"displayName":"Strong"}`)

	data, err := repo.Get(context.Background(), rulesetID, "traits", "strong")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// Compare parsed JSON rather than raw bytes: the column is JSONB, which
	// canonicalizes its text form (e.g. inserts a space after ':') rather than
	// preserving the exact bytes that were inserted.
	var got, want any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal got data %s: %v", data, err)
	}
	if err := json.Unmarshal([]byte(`{"displayName":"Strong"}`), &want); err != nil {
		t.Fatalf("unmarshal want data: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %s, want the seeded payload", data)
	}
}

func TestRepository_Get_NotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := rulesettables.NewRepository(pool)

	_, err := repo.Get(context.Background(), uuid.New(), "traits", "nonexistent")
	if !errors.Is(err, rulesettables.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestRepository_Get_ReturnsNewestVersion_WhenRowHasMultipleContentHashes(t *testing.T) {
	pool := newTestPool(t)
	repo := rulesettables.NewRepository(pool)
	rulesetID := uuid.New()
	older := time.Now().Add(-time.Hour)
	newer := time.Now()

	seedRowAt(t, pool, rulesetID, "traits", "strong", "hash-v1", `{"displayName":"Strong"}`, older)
	seedRowAt(t, pool, rulesetID, "traits", "strong", "hash-v2", `{"displayName":"Really Strong"}`, newer)

	data, err := repo.Get(context.Background(), rulesetID, "traits", "strong")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var got struct {
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.DisplayName != "Really Strong" {
		t.Fatalf("got %q, want the newer version's displayName %q", got.DisplayName, "Really Strong")
	}
}

func TestRepository_List_ReturnsNewestVersionPerKey(t *testing.T) {
	pool := newTestPool(t)
	repo := rulesettables.NewRepository(pool)
	rulesetID := uuid.New()
	older := time.Now().Add(-time.Hour)
	newer := time.Now()

	seedRowAt(t, pool, rulesetID, "traits", "strong", "hash-v1", `{"displayName":"Strong"}`, older)
	seedRowAt(t, pool, rulesetID, "traits", "strong", "hash-v2", `{"displayName":"Really Strong"}`, newer)
	seedRowAt(t, pool, rulesetID, "traits", "agile", "hash-a1", `{"displayName":"Agile"}`, older)

	rows, err := repo.List(context.Background(), rulesetID, "traits")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (one per row_key, not one per content_hash version)", len(rows))
	}
	if rows[0].Key != "agile" || rows[1].Key != "strong" {
		t.Fatalf("got keys [%s %s], want [agile strong]", rows[0].Key, rows[1].Key)
	}
	var strongData struct {
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(rows[1].Data, &strongData); err != nil {
		t.Fatalf("unmarshal strong: %v", err)
	}
	if strongData.DisplayName != "Really Strong" {
		t.Fatalf("got %q for strong, want the newer version", strongData.DisplayName)
	}
}
