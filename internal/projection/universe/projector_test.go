package universe_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/universe/events"
	"github.com/timadorus/platform/internal/projection"
	universeprojection "github.com/timadorus/platform/internal/projection/universe"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../checkpoint/migrations/0001_projection_checkpoints.up.sql",
			"migrations/0001_universe_read_model.up.sql",
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

// TestProjector_IdempotentReplay delivers the same UniverseCreated message to the Router
// twice (simulating NATS at-least-once redelivery) and asserts the read model — including
// the universe_creators junction table — ends up identical to a single delivery, per plan
// §11's idempotent-replay requirement.
func TestProjector_IdempotentReplay(t *testing.T) {
	pool := newTestPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// gochannel is an in-memory Watermill pub/sub, interface-compatible with the NATS
	// implementation used in production — this is what lets Router stay decoupled from NATS
	// (see internal/projection.SubscriberFactory).
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })

	p := universeprojection.NewProjector()
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())

	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	universeID := uuid.New()
	creatorID := uuid.New()
	envelope := bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   universeID,
		AggregateType: events.AggregateType,
		Version:       1,
		EventType:     events.TypeUniverseCreated,
		Payload: mustMarshal(t, events.UniverseCreated{
			ID:             universeID,
			Name:           "Krynn",
			CreatorUserIDs: []uuid.UUID{creatorID},
			OccurredAt:     time.Now().UTC(),
		}),
	}
	body := mustMarshal(t, envelope)

	subject := bus.Subject(events.AggregateType)
	publish := func() {
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(subject, msg); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	publish()
	waitForRow(t, pool, universeID)

	// Deliver the same event a second time.
	publish()
	// There's no ack signal to wait on from here, so give the router a moment to process
	// (and potentially mis-apply) the redelivery before asserting.
	time.Sleep(300 * time.Millisecond)

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM universes_read_model WHERE id = $1`, universeID).Scan(&count); err != nil {
		t.Fatalf("count universes_read_model rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d universes_read_model rows, want 1 (duplicate delivery must be a no-op)", count)
	}

	var creatorCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM universe_creators WHERE universe_id = $1`, universeID).Scan(&creatorCount); err != nil {
		t.Fatalf("count universe_creators rows: %v", err)
	}
	if creatorCount != 1 {
		t.Fatalf("got %d universe_creators rows, want 1 (duplicate delivery must not duplicate junction rows)", creatorCount)
	}

	var lastSeq int64
	if err := pool.QueryRow(ctx, `SELECT last_global_seq FROM projection_checkpoints WHERE projection_name = $1`, p.Name()).Scan(&lastSeq); err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	if lastSeq != 1 {
		t.Fatalf("got checkpoint %d, want 1 (redelivery must not double-advance it)", lastSeq)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

func waitForRow(t *testing.T, pool *pgxpool.Pool, universeID uuid.UUID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM universes_read_model WHERE id = $1`, universeID,
		).Scan(&count); err != nil {
			t.Fatalf("poll universes_read_model: %v", err)
		}
		if count == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for first delivery to be projected")
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
