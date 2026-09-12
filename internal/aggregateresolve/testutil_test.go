package aggregateresolve_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../projection/campaign/migrations/0001_campaign_read_model.up.sql",
			"../projection/campaign/migrations/0002_campaign_ruleset_id.up.sql",
			"../projection/campaign/migrations/0003_campaign_configuration.up.sql",
			"../projection/entity/migrations/0001_entity_read_model.up.sql",
			"../projection/object/migrations/0001_object_read_model.up.sql",
			"../projection/character/migrations/0001_character_read_model.up.sql",
			"../projection/character/migrations/0002_character_info.up.sql",
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

// seedCampaign inserts a minimal campaigns_read_model row — shared by every test that needs an
// existing Campaign to resolve a Character/Campaign event's UniverseID against.
func seedCampaign(t *testing.T, pool *pgxpool.Pool, campaignID, universeID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, configuration, is_archived, updated_at)
		 VALUES ($1, 'Test Campaign', $2, $3, '', false, now())`,
		campaignID, universeID, uuid.New(),
	); err != nil {
		t.Fatalf("seed campaigns_read_model: %v", err)
	}
}
