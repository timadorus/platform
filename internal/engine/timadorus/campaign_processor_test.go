package timadorus_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign"
	"github.com/timadorus/platform/internal/domain/campaign/events"
	"github.com/timadorus/platform/internal/domain/ruleset"
	rulesetevents "github.com/timadorus/platform/internal/domain/ruleset/events"
	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
	"github.com/timadorus/platform/internal/projection"
)

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

func rulesetRepo(pool *pgxpool.Pool) *eventsourcing.Repository[*ruleset.Ruleset] {
	registry := eventsourcing.NewRegistry()
	rulesetevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	return eventsourcing.NewRepository(store, ruleset.AggregateType, func() *ruleset.Ruleset {
		return &ruleset.Ruleset{}
	})
}

// createRuleset drives a real Ruleset through the real event store (unlike seedRuleset, which
// only inserts a rulesets_read_model row with no corresponding event) — needed by any test whose
// CampaignCreated handling now resolves the Ruleset name via the event store rather than the read
// model. Returns the server-generated id, which the caller passes to campaign.New/createCampaign
// as rulesetID.
func createRuleset(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	repo := rulesetRepo(pool)
	rs, err := ruleset.New(name, "", nil)
	if err != nil {
		t.Fatalf("ruleset.New: %v", err)
	}
	if err := repo.Save(context.Background(), rs); err != nil {
		t.Fatalf("save ruleset: %v", err)
	}
	return rs.AggregateID()
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

func TestCampaignProcessor_MatchingRuleset_AppendsTimestamp(t *testing.T) {
	pool := newTestPool(t)

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
		Payload:       mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: occurredAt}),
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
	pool := newTestPool(t)

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
		Payload:       mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
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
	pool := newTestPool(t)

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
		Payload:       mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
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
	pool := newTestPool(t)

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
		Payload:       mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
		Metadata:      mustMarshal(t, map[string]string{"correlation_id": correlationID, "causation_id": correlationID}),
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

func TestCampaignProcessor_CampaignCreated_MatchingRuleset_MergesDefaultTraits(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := createRuleset(t, pool, "TIMADORUS") // exact-case mismatch on purpose

	ctx := context.Background()
	repo := campaignRepo(pool)
	c, err := campaign.New(uuid.New(), rulesetID, "Test Campaign", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("campaign.New: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save campaign: %v", err)
	}
	campaignID := c.AggregateID()
	// Deliberately NOT seeding campaigns_read_model or rulesets_read_model — proves
	// handleCampaignCreated's ruleset lookup does not depend on either table's row existing yet
	// (the real race this plan fixes), since the Ruleset comes from the event store instead.

	publish, wait := runCampaignEngine(t, pool)

	occurredAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       1,
		EventType:     events.TypeCampaignCreated,
		Payload: mustMarshal(t, events.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: c.UniverseID(), RulesetID: rulesetID,
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: occurredAt,
		}),
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
		Traits []string `json:"traits"`
	}
	if err := json.Unmarshal([]byte(configEvent.Configuration), &decoded); err != nil {
		t.Fatalf("configuration %q is not the expected shape: %v", configEvent.Configuration, err)
	}
	want := []string{"strong", "agile", "quick"}
	if len(decoded.Traits) != len(want) {
		t.Fatalf("got traits %v, want %v", decoded.Traits, want)
	}
	for i := range want {
		if decoded.Traits[i] != want[i] {
			t.Fatalf("got traits %v, want %v", decoded.Traits, want)
		}
	}

	wait()
}

func TestCampaignProcessor_CampaignCreated_NonMatchingRuleset_NoOp(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := createRuleset(t, pool, "SomethingElse")

	ctx := context.Background()
	repo := campaignRepo(pool)
	c, err := campaign.New(uuid.New(), rulesetID, "Test Campaign", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("campaign.New: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save campaign: %v", err)
	}
	campaignID := c.AggregateID()

	publish, wait := runCampaignEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       1,
		EventType:     events.TypeCampaignCreated,
		Payload: mustMarshal(t, events.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: c.UniverseID(), RulesetID: rulesetID,
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC(),
		}),
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

func TestCampaignProcessor_TraitsAndConfigsCoexist(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := createRuleset(t, pool, "timadorus")
	// Also seed rulesets_read_model directly (same id, same name): this test's second leg
	// exercises ConfigurationRequested, whose ruleset resolution (RulesetCache.resolve) still
	// depends on rulesets_read_model's join, regardless of whether the cache was already warmed
	// by the first (CampaignCreated) leg.
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID) // seeds campaigns_read_model too, fine here

	publish, wait := runCampaignEngine(t, pool)

	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: events.AggregateType, Version: 1,
		EventType: events.TypeCampaignCreated,
		Payload: mustMarshal(t, events.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: uuid.New(), RulesetID: rulesetID,
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: createdAt,
		}),
	})

	// Wait for the traits-driven ConfigurationChanged before publishing the second event, so the
	// two don't race each other for who loads/saves the aggregate first.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&count); err != nil {
			t.Fatalf("count configuration_changed events: %v", err)
		} else if count == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	occurredAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	publish(bus.Envelope{
		GlobalSeq: 2, AggregateID: campaignID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeConfigurationRequested,
		Payload:   mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: occurredAt}),
	})

	// Wait specifically for the *second* ConfigurationChanged row (count == 2), not merely for
	// "a row exists" — a row from the first (traits) event already satisfies that weaker check,
	// which would let this loop return immediately on its very first iteration, before the
	// second event has actually been processed, and read the stale pre-append payload back.
	deadline = time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&count); err != nil {
			t.Fatalf("count configuration_changed events: %v", err)
		}
		if count >= 2 {
			if err := pool.QueryRow(context.Background(),
				`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2
				 ORDER BY version DESC LIMIT 1`,
				campaignID, events.TypeConfigurationChanged,
			).Scan(&payload); err != nil {
				t.Fatalf("query configuration_changed event: %v", err)
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if payload == nil {
		t.Fatal("timed out waiting for the second campaign.configuration_changed.v1 event")
	}

	var configEvent struct {
		Configuration string `json:"configuration"`
	}
	if err := json.Unmarshal(payload, &configEvent); err != nil {
		t.Fatalf("configuration_changed payload %s is not the expected shape: %v", payload, err)
	}

	var decoded struct {
		Traits  []string `json:"traits"`
		Configs []string `json:"configs"`
	}
	if err := json.Unmarshal([]byte(configEvent.Configuration), &decoded); err != nil {
		t.Fatalf("configuration %q is not the expected shape: %v", configEvent.Configuration, err)
	}
	wantTraits := []string{"strong", "agile", "quick"}
	if len(decoded.Traits) != len(wantTraits) {
		t.Fatalf("got traits %v, want %v (traits should survive the configs append)", decoded.Traits, wantTraits)
	}
	for i := range wantTraits {
		if decoded.Traits[i] != wantTraits[i] {
			t.Fatalf("got traits %v, want %v (traits should survive the configs append)", decoded.Traits, wantTraits)
		}
	}
	if len(decoded.Configs) != 1 || decoded.Configs[0] != occurredAt.Format(time.RFC3339Nano) {
		t.Fatalf("got configs %v, want [%q]", decoded.Configs, occurredAt.Format(time.RFC3339Nano))
	}

	wait()
}

func TestCampaignProcessor_CampaignCreated_ArchivedCampaign_NoOp(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := createRuleset(t, pool, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)
	archiveCampaign(t, pool, campaignID)

	publish, wait := runCampaignEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: events.AggregateType, Version: 1,
		EventType: events.TypeCampaignCreated,
		Payload: mustMarshal(t, events.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: uuid.New(), RulesetID: rulesetID,
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC(),
		}),
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
