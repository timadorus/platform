package ruleset_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	rulesetcmd "github.com/timadorus/platform/internal/command/ruleset"
	"github.com/timadorus/platform/internal/domain/ruleset"
	rulesetevents "github.com/timadorus/platform/internal/domain/ruleset/events"
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
		tcpostgres.WithOrderedInitScripts(
			"../../eventstore/postgres/migrations/0001_events.up.sql",
			"../../eventstore/postgres/migrations/0002_outbox.up.sql",
			"migrations/0001_ruleset_names.up.sql",
			"migrations/0002_ruleset_names_id.up.sql",
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

func newService(t *testing.T, pool *pgxpool.Pool) *rulesetcmd.Service {
	t.Helper()
	registry := eventsourcing.NewRegistry()
	rulesetevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, ruleset.AggregateType, func() *ruleset.Ruleset {
		return &ruleset.Ruleset{}
	})
	return rulesetcmd.NewService(repo, pool)
}

func TestService_Create_Succeeds(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	id, err := service.Create(ctx, "D&D 5e", "a description", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == uuid.Nil {
		t.Fatal("got nil id")
	}
}

func TestService_Create_DuplicateName_ReturnsErrNameAlreadyExists(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	if _, err := service.Create(ctx, "Pathfinder", "", nil); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := service.Create(ctx, "Pathfinder", "a different description", nil)
	if !errors.Is(err, ruleset.ErrNameAlreadyExists) {
		t.Fatalf("got %v, want ErrNameAlreadyExists", err)
	}

	var eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM events WHERE aggregate_type = $1 AND event_type = $2`,
		ruleset.AggregateType, rulesetevents.TypeRulesetCreated,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("got %d RulesetCreated events, want 1 (duplicate must not create a second aggregate)", eventCount)
	}

	var nameCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ruleset_names WHERE name = $1`, "Pathfinder").Scan(&nameCount); err != nil {
		t.Fatalf("count ruleset_names: %v", err)
	}
	if nameCount != 1 {
		t.Fatalf("got %d ruleset_names rows for %q, want 1", nameCount, "Pathfinder")
	}
}

func TestService_Create_EmptyName_FailsBeforeTouchingDB(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	_, err := service.Create(ctx, "", "", nil)
	if !errors.Is(err, ruleset.ErrNameRequired) {
		t.Fatalf("got %v, want ErrNameRequired", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ruleset_names`).Scan(&count); err != nil {
		t.Fatalf("count ruleset_names: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d ruleset_names rows, want 0 (validation must run before any DB access)", count)
	}
}

func TestService_FindIDByName_ReturnsTheIDCreateReserved(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	id, err := service.Create(ctx, "GURPS", "", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := service.FindIDByName(ctx, "GURPS")
	if err != nil {
		t.Fatalf("find id by name: %v", err)
	}
	if found != id {
		t.Fatalf("got id %s, want %s", found, id)
	}
}

func TestService_FindIDByName_UnknownName_ReturnsError(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	if _, err := service.FindIDByName(ctx, "NeverCreated"); err == nil {
		t.Fatal("got nil error for an unreserved name, want a real error")
	}
}

func TestService_Rename_ReleasesOldNameAndReservesNew(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	id, err := service.Create(ctx, "OldName", "", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := service.Rename(ctx, id, "NewName"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// The old name must be released: a later Create must succeed, not collide.
	if _, err := service.Create(ctx, "OldName", "", nil); err != nil {
		t.Fatalf("create with the released old name: %v", err)
	}

	// The new name must be reserved: FindIDByName must resolve it to the renamed aggregate.
	found, err := service.FindIDByName(ctx, "NewName")
	if err != nil {
		t.Fatalf("find id by new name: %v", err)
	}
	if found != id {
		t.Fatalf("got id %s for \"NewName\", want %s", found, id)
	}

	var oldNameCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ruleset_names WHERE name = $1 AND id = $2`, "OldName", id).Scan(&oldNameCount); err != nil {
		t.Fatalf("count old reservation still pointing at the renamed aggregate: %v", err)
	}
	if oldNameCount != 0 {
		t.Fatalf("got %d ruleset_names rows for (\"OldName\", %s), want 0 (must not still point at the renamed aggregate)", oldNameCount, id)
	}
}

func TestService_Rename_ToAnAlreadyTakenName_FailsAndKeepsOldReservation(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	idA, err := service.Create(ctx, "RulesetA", "", nil)
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, err := service.Create(ctx, "RulesetB", "", nil); err != nil {
		t.Fatalf("create B: %v", err)
	}

	err = service.Rename(ctx, idA, "RulesetB")
	if !errors.Is(err, ruleset.ErrNameAlreadyExists) {
		t.Fatalf("got %v, want ErrNameAlreadyExists", err)
	}

	// A's old reservation must survive the failed rename attempt.
	found, err := service.FindIDByName(ctx, "RulesetA")
	if err != nil {
		t.Fatalf("find id by RulesetA after failed rename: %v", err)
	}
	if found != idA {
		t.Fatalf("got id %s for \"RulesetA\" after failed rename, want %s (old reservation must survive)", found, idA)
	}

	var eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		idA, rulesetevents.TypeRulesetRenamed,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count RulesetRenamed events: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("got %d RulesetRenamed events for A, want 0 (a failed reservation must not still raise the event)", eventCount)
	}
}

func TestService_Rename_ToTheSameName_IsANoOpAndDoesNotTouchReservations(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	id, err := service.Create(ctx, "SameName", "", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := service.Rename(ctx, id, "SameName"); err != nil {
		t.Fatalf("rename to the same name: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ruleset_names WHERE name = $1`, "SameName").Scan(&count); err != nil {
		t.Fatalf("count ruleset_names: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d ruleset_names rows for \"SameName\", want 1 (renaming to the current name must be a pure no-op)", count)
	}
}
