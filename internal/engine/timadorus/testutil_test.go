package timadorus_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/engine/timadorus/tables"
)

// newTestPool starts a fresh Postgres testcontainer with every migration this package's tests
// need — Character's, Campaign's, and Ruleset's (read by RulesetCache.resolve), plus the shared
// checkpoint tables and the Ruleset name-reservation table Task 3's register_test.go needs — so
// every test file in this package can share one pool constructor instead of each defining its
// own subset. Those subsets had drifted out of sync with each other (one was missing Campaign's
// 0003 configuration column; another had no Character migrations at all and was replaced by a
// hand-rolled inline CREATE TABLE that could silently diverge from the real schema the next time
// characters_read_model changed).
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../../eventstore/postgres/migrations/0001_events.up.sql",
			"../../eventstore/postgres/migrations/0002_outbox.up.sql",
			"../../projection/checkpoint/migrations/0001_projection_checkpoints.up.sql",
			"../../projection/checkpoint/migrations/0002_projection_dead_letters.up.sql",
			"../../projection/character/migrations/0001_character_read_model.up.sql",
			"../../projection/character/migrations/0002_character_info.up.sql",
			"../../projection/campaign/migrations/0001_campaign_read_model.up.sql",
			"../../projection/campaign/migrations/0002_campaign_ruleset_id.up.sql",
			"../../projection/campaign/migrations/0003_campaign_configuration.up.sql",
			"../../projection/ruleset/migrations/0001_ruleset_read_model.up.sql",
			"../../command/ruleset/migrations/0001_ruleset_names.up.sql",
			"../../command/ruleset/migrations/0002_ruleset_names_id.up.sql",
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

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestTraitsTable loads traits.yaml and registers every production trait hook onto it, exactly
// like cmd/timadorus-engine's own startup (tables.LoadTraits + timadorus.RegisterTraitHooks) — so
// every test's CharacterProcessor behaves identically to production instead of running against an
// empty, hookless traits table.
func newTestTraitsTable(t *testing.T) *tables.TraitsTable {
	t.Helper()
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits table: %v", err)
	}
	if err := timadorus.RegisterTraitHooks(traits); err != nil {
		t.Fatalf("register trait hooks: %v", err)
	}
	return traits
}
