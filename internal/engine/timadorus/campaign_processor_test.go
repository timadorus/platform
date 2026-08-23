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
	"github.com/timadorus/platform/internal/domain/campaign"
	"github.com/timadorus/platform/internal/domain/campaign/events"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
	"github.com/timadorus/platform/internal/projection"
)

func newCampaignTestPool(t *testing.T) *pgxpool.Pool {
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
			"../../projection/campaign/migrations/0001_campaign_read_model.up.sql",
			"../../projection/campaign/migrations/0002_campaign_ruleset_id.up.sql",
			"../../projection/campaign/migrations/0003_campaign_configuration.up.sql",
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

func campaignRepo(pool *pgxpool.Pool) *eventsourcing.Repository[*campaign.Campaign] {
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	return eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
}

// createCampaign drives a real Campaign through the real event store, then seeds the
// campaigns_read_model row (which the projector would normally populate) with rulesetID so the
// Processor's ruleset lookup has something to join against.
func createCampaign(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	repo := campaignRepo(pool)

	c, err := campaign.New(uuid.New(), rulesetID, "Test Campaign", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("campaign.New: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save campaign: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, configuration, is_archived, updated_at)
		 VALUES ($1, $2, $3, $4, '', false, now())`,
		c.AggregateID(), c.Name(), c.UniverseID(), rulesetID,
	); err != nil {
		t.Fatalf("seed campaigns_read_model: %v", err)
	}
	return c.AggregateID()
}

func seedRuleset(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID, name string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO rulesets_read_model (id, name, description, reference_urls, is_archived, updated_at)
		 VALUES ($1, $2, '', '{}', false, now())`, rulesetID, name,
	); err != nil {
		t.Fatalf("seed ruleset: %v", err)
	}
}

func archiveCampaign(t *testing.T, pool *pgxpool.Pool, campaignID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	repo := campaignRepo(pool)

	c, err := repo.Load(ctx, campaignID)
	if err != nil {
		t.Fatalf("load campaign: %v", err)
	}
	if err := c.Archive(); err != nil {
		t.Fatalf("archive campaign: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save archived campaign: %v", err)
	}
}

func runCampaignEngine(t *testing.T, pool *pgxpool.Pool) (publish func(env bus.Envelope), wait func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })

	p := timadorus.NewCampaignProcessor(pool, timadorus.NewRulesetCache())
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardCampaignLogger())

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

func TestCampaignProcessor_MatchingRuleset_AppendsTimestamp(t *testing.T) {
	pool := newCampaignTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "TIMADORUS") // exact-case mismatch on purpose
	campaignID := createCampaign(t, pool, rulesetID)

	publish, wait := runCampaignEngine(t, pool)

	occurredAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshalCampaign(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: occurredAt}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&payload)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query configuration_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if payload == nil {
		t.Fatal("timed out waiting for a campaign.configuration_changed.v1 event")
	}

	var configEvent struct {
		Configuration string `json:"configuration"`
	}
	if err := json.Unmarshal(payload, &configEvent); err != nil {
		t.Fatalf("configuration_changed payload %s is not the expected shape: %v", payload, err)
	}

	var decoded struct {
		Configs []string `json:"configs"`
	}
	if err := json.Unmarshal([]byte(configEvent.Configuration), &decoded); err != nil {
		t.Fatalf("configuration %q is not the expected shape: %v", configEvent.Configuration, err)
	}
	if len(decoded.Configs) != 1 || decoded.Configs[0] != occurredAt.Format(time.RFC3339Nano) {
		t.Fatalf("got configs %v, want [%q]", decoded.Configs, occurredAt.Format(time.RFC3339Nano))
	}

	wait()
}

func TestCampaignProcessor_NonMatchingRuleset_NoOp(t *testing.T) {
	pool := newCampaignTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "SomethingElse")
	campaignID := createCampaign(t, pool, rulesetID)

	publish, wait := runCampaignEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshalCampaign(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})

	time.Sleep(500 * time.Millisecond)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		campaignID, events.TypeConfigurationChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count configuration_changed events: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d campaign.configuration_changed.v1 events, want 0 (ruleset doesn't match)", count)
	}

	wait()
}

func TestCampaignProcessor_ArchivedCampaign_NoOp(t *testing.T) {
	pool := newCampaignTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)
	archiveCampaign(t, pool, campaignID)

	publish, wait := runCampaignEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshalCampaign(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})

	time.Sleep(500 * time.Millisecond)

	var configChangedCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		campaignID, events.TypeConfigurationChanged,
	).Scan(&configChangedCount); err != nil {
		t.Fatalf("count configuration_changed events: %v", err)
	}
	if configChangedCount != 0 {
		t.Fatalf("got %d campaign.configuration_changed.v1 events, want 0 (campaign is archived)", configChangedCount)
	}

	var deadLetterCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM projection_dead_letters WHERE aggregate_id = $1`,
		campaignID,
	).Scan(&deadLetterCount); err != nil {
		t.Fatalf("count dead letters: %v", err)
	}
	if deadLetterCount != 0 {
		t.Fatalf("got %d dead-lettered events, want 0 (archived Campaign should be a clean no-op)", deadLetterCount)
	}

	wait()
}

func TestCampaignProcessor_PreservesCorrelationID(t *testing.T) {
	pool := newCampaignTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)

	publish, wait := runCampaignEngine(t, pool)

	const correlationID = "test-campaign-correlation-id-12345"
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshalCampaign(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
		Metadata:      mustMarshalCampaign(t, map[string]string{"correlation_id": correlationID, "causation_id": correlationID}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var metadata []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT metadata FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&metadata)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query configuration_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if metadata == nil {
		t.Fatal("timed out waiting for a campaign.configuration_changed.v1 event")
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

// TestSharedRulesetCache_ServesBothProcessors proves the design's central claim: one
// RulesetCache instance, populated by a Campaign-triggered lookup, is then used as-is by a
// Character-triggered lookup for the same campaign — without a second query — even after the
// underlying ruleset name changes in Postgres. This exercises both processors together, wired
// exactly as cmd/timadorus-engine/main.go wires them.
func TestSharedRulesetCache_ServesBothProcessors(t *testing.T) {
	pool := newCampaignTestPool(t)
	// Character migrations are needed too since this test also drives a Character-triggered
	// lookup through CharacterProcessor.
	if _, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS characters_read_model (
			id UUID PRIMARY KEY, name TEXT NOT NULL, campaign_id UUID NOT NULL,
			entity_id UUID NOT NULL, player_user_id UUID NOT NULL, info TEXT NOT NULL DEFAULT '',
			is_archived BOOLEAN NOT NULL DEFAULT false, updated_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		t.Fatalf("create characters_read_model: %v", err)
	}

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)

	cache := timadorus.NewRulesetCache()
	campaignProcessor := timadorus.NewCampaignProcessor(pool, cache)
	characterProcessor := timadorus.NewCharacterProcessor(pool, cache)

	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardCampaignLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{campaignProcessor, characterProcessor}) }()

	publish := func(subject string, env bus.Envelope) {
		body := mustMarshalCampaign(t, env)
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(subject, msg); err != nil {
			t.Fatalf("publish to %s: %v", subject, err)
		}
	}

	// First: a Campaign-triggered lookup populates the shared cache with "timadorus".
	publish(bus.Subject(events.AggregateType), bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeConfigurationRequested,
		Payload:   mustMarshalCampaign(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})
	waitForConfigurationChanged(t, pool, campaignID)

	// Mutate the ruleset's name in Postgres directly. If CharacterProcessor re-queried instead
	// of using the shared cache, it would see this new name and (correctly, per its own logic)
	// decide not to act — so a Character-triggered append succeeding after this proves the
	// cache, not a fresh query, served the lookup.
	if _, err := pool.Exec(context.Background(),
		`UPDATE rulesets_read_model SET name = 'ChangedLater' WHERE id = $1`, rulesetID,
	); err != nil {
		t.Fatalf("mutate ruleset name: %v", err)
	}

	// Drive a real Character through the real event store (createCharacter, from
	// character_processor_test.go in this same test package) rather than only seeding
	// characters_read_model: CharacterProcessor.Handle loads the Character aggregate itself
	// (p.characters.Load), which requires actual events in the store, not just a read-model row.
	characterID := createCharacter(t, pool, campaignID)

	publish(bus.Subject(characterevents.AggregateType), bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: characterevents.AggregateType, Version: 2,
		EventType: characterevents.TypeActionRequested,
		Payload:   mustMarshalCampaign(t, characterevents.ActionRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
			characterID, characterevents.TypeInfoChanged,
		).Scan(&count); err != nil {
			t.Fatalf("poll info_changed: %v", err)
		}
		if count == 1 {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatal("timed out waiting for character.info_changed.v1 — CharacterProcessor did not use the shared cache (it must have re-queried and seen the changed ruleset name)")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

func waitForConfigurationChanged(t *testing.T, pool *pgxpool.Pool, campaignID uuid.UUID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&count); err != nil {
			t.Fatalf("poll configuration_changed: %v", err)
		}
		if count == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for campaign.configuration_changed.v1")
}

func mustMarshalCampaign(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func discardCampaignLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
