package timadorus_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/character"
	"github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
	"github.com/timadorus/platform/internal/projection"
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
			"../../projection/checkpoint/migrations/0001_projection_checkpoints.up.sql",
			"../../projection/checkpoint/migrations/0002_projection_dead_letters.up.sql",
			"../../projection/character/migrations/0001_character_read_model.up.sql",
			"../../projection/character/migrations/0002_character_info.up.sql",
			"../../projection/campaign/migrations/0001_campaign_read_model.up.sql",
			"../../projection/campaign/migrations/0002_campaign_ruleset_id.up.sql",
			"../../projection/ruleset/migrations/0001_ruleset_read_model.up.sql",
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

// seedCampaignAndRuleset inserts directly into the read-model tables the Processor reads from
// — the Processor never touches the Campaign/Ruleset aggregates or their own event streams, so
// building real aggregates here would test more than this package owns.
func seedCampaignAndRuleset(t *testing.T, pool *pgxpool.Pool, campaignID, rulesetID uuid.UUID, rulesetName string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO rulesets_read_model (id, name, description, reference_urls, is_archived, updated_at)
		 VALUES ($1, $2, '', '{}', false, now())`, rulesetID, rulesetName,
	); err != nil {
		t.Fatalf("seed ruleset: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, is_archived, updated_at)
		 VALUES ($1, 'Test Campaign', $2, $3, false, now())`, campaignID, uuid.New(), rulesetID,
	); err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
}

// createCharacter drives a real Character through the real event store (postgres.NewStore) —
// exactly what command-api's own CreateCharacter flow does, minus the Entity half — so it ends
// up with a real events/outbox row and a real characters_read_model row.
func createCharacter(t *testing.T, pool *pgxpool.Pool, campaignID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})

	c, err := character.New(campaignID, uuid.New(), uuid.New(), "Elminster")
	if err != nil {
		t.Fatalf("character.New: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save character: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO characters_read_model (id, name, campaign_id, entity_id, player_user_id, info, is_archived, updated_at)
		 VALUES ($1, $2, $3, $4, $5, '', false, now())`,
		c.AggregateID(), c.Name(), c.CampaignID(), c.EntityID(), c.PlayerUserID(),
	); err != nil {
		t.Fatalf("seed characters_read_model: %v", err)
	}
	return c.AggregateID()
}

// archiveCharacter loads characterID through the same repository pattern createCharacter
// uses, archives it, and saves it back through the real event store — exactly what
// command-api's own ArchiveCharacter flow does — so the Processor sees a genuinely archived
// aggregate when it later loads this Character.
func archiveCharacter(t *testing.T, pool *pgxpool.Pool, characterID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})

	c, err := repo.Load(ctx, characterID)
	if err != nil {
		t.Fatalf("load character: %v", err)
	}
	if err := c.Archive(); err != nil {
		t.Fatalf("archive character: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save archived character: %v", err)
	}
}

func runEngine(t *testing.T, pool *pgxpool.Pool) (publish func(env bus.Envelope), wait func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })

	p := timadorus.NewProcessor(pool)
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())

	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	subject := bus.Subject(events.AggregateType)
	publish = func(env bus.Envelope) {
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(subject, msg); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	wait = func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("router.Run: %v", err)
		}
	}
	return publish, wait
}

func TestProcessor_MatchingRuleset_AppendsTimestamp(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "TIMADORUS") // exact-case mismatch on purpose
	characterID := createCharacter(t, pool, campaignID)

	publish, wait := runEngine(t, pool)

	occurredAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   characterID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeActionRequested,
		Payload:       mustMarshal(t, events.ActionRequested{Payload: "{}", OccurredAt: occurredAt}),
	})

	// The Processor's actual write is a new character.info_changed.v1 event appended to the
	// event store via SetInfo/Save — characters_read_model.info itself is only ever updated
	// later by the separate Character read-model projector consuming that event off the
	// outbox/bus, and neither that projector nor an outbox relay runs in this test, so the
	// read model would never change; assert against the event store, what this package
	// actually owns.
	deadline := time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			characterID, events.TypeInfoChanged,
		).Scan(&payload)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query info_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if payload == nil {
		t.Fatal("timed out waiting for a character.info_changed.v1 event")
	}

	var infoEvent struct {
		Info string `json:"info"`
	}
	if err := json.Unmarshal(payload, &infoEvent); err != nil {
		t.Fatalf("info_changed payload %s is not the expected shape: %v", payload, err)
	}

	var decoded struct {
		Actions []string `json:"actions"`
	}
	if err := json.Unmarshal([]byte(infoEvent.Info), &decoded); err != nil {
		t.Fatalf("info %q is not the expected shape: %v", infoEvent.Info, err)
	}
	if len(decoded.Actions) != 1 || decoded.Actions[0] != occurredAt.Format(time.RFC3339Nano) {
		t.Fatalf("got actions %v, want [%q]", decoded.Actions, occurredAt.Format(time.RFC3339Nano))
	}

	wait()
}

func TestProcessor_NonMatchingRuleset_NoOp(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "SomethingElse")
	characterID := createCharacter(t, pool, campaignID)

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   characterID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeActionRequested,
		Payload:       mustMarshal(t, events.ActionRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})

	// No success signal to wait on for a deliberate no-op, so give the router a moment before
	// asserting nothing changed.
	time.Sleep(500 * time.Millisecond)

	// A no-op must never append a character.info_changed.v1 event — the Character's event
	// stream should still hold only its original character.created.v1 event.
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d character.info_changed.v1 events, want 0 (ruleset doesn't match)", count)
	}

	wait()
}

// TestProcessor_ArchivedCharacter_NoOp covers final-review finding 1: a Character archived
// between its PUT .../action request and this engine processing the resulting
// ActionRequested must be a clean no-op, not a permanent dead-letter — retrying can't
// un-archive the aggregate, so Handle must swallow character.ErrArchived rather than fail.
func TestProcessor_ArchivedCharacter_NoOp(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	characterID := createCharacter(t, pool, campaignID)
	archiveCharacter(t, pool, characterID)

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   characterID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeActionRequested,
		Payload:       mustMarshal(t, events.ActionRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})

	// No success signal to wait on for a deliberate no-op, so give the router a moment before
	// asserting nothing changed.
	time.Sleep(500 * time.Millisecond)

	// A no-op must never append a character.info_changed.v1 event.
	var infoChangedCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&infoChangedCount); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if infoChangedCount != 0 {
		t.Fatalf("got %d character.info_changed.v1 events, want 0 (character is archived)", infoChangedCount)
	}

	// And the router must have kept processing cleanly — no dead-letter row for this event.
	var deadLetterCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM projection_dead_letters WHERE aggregate_id = $1`,
		characterID,
	).Scan(&deadLetterCount); err != nil {
		t.Fatalf("count dead letters: %v", err)
	}
	if deadLetterCount != 0 {
		t.Fatalf("got %d dead-lettered events, want 0 (archived Character should be a clean no-op)", deadLetterCount)
	}

	wait()
}

// TestProcessor_PreservesCorrelationID covers final-review finding 2: the derived
// InfoChanged event must carry forward the correlation id from the originating
// ActionRequested envelope, not lose it to the Router's own (correlation-less) context.
func TestProcessor_PreservesCorrelationID(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	characterID := createCharacter(t, pool, campaignID)

	publish, wait := runEngine(t, pool)

	const correlationID = "test-correlation-id-12345"
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   characterID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeActionRequested,
		Payload:       mustMarshal(t, events.ActionRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
		Metadata:      mustMarshal(t, map[string]string{"correlation_id": correlationID, "causation_id": correlationID}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var metadata []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT metadata FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			characterID, events.TypeInfoChanged,
		).Scan(&metadata)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query info_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if metadata == nil {
		t.Fatal("timed out waiting for a character.info_changed.v1 event")
	}

	var decoded struct {
		CorrelationID string `json:"correlation_id"`
	}
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		t.Fatalf("metadata %s is not the expected shape: %v", metadata, err)
	}
	if decoded.CorrelationID != correlationID {
		t.Fatalf("got correlation_id %q, want %q", decoded.CorrelationID, correlationID)
	}

	wait()
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
