package timadorus_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
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
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	"github.com/timadorus/platform/internal/domain/character"
	"github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
	"github.com/timadorus/platform/internal/projection"
)

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

// seedRealCampaign creates a real Campaign aggregate (via campaign.New, with SetConfiguration
// applied if configuration is non-empty) and saves it through the real event store, then seeds
// the matching campaigns_read_model/rulesets_read_model rows via seedCampaignAndRuleset (still
// needed for RulesetCache.resolve's ruleset-name lookup, unchanged by this fix) — needed now that
// handleCharacterCreated loads the write-side Campaign aggregate directly instead of reading
// campaigns_read_model.configuration.
func seedRealCampaign(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID, rulesetName, configuration string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	registry := eventsourcing.NewRegistry()
	campaignevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})

	c, err := campaign.New(uuid.New(), rulesetID, "Test Campaign", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("campaign.New: %v", err)
	}
	if configuration != "" {
		if err := c.SetConfiguration(configuration); err != nil {
			t.Fatalf("set configuration: %v", err)
		}
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save campaign: %v", err)
	}

	seedCampaignAndRuleset(t, pool, c.AggregateID(), rulesetID, rulesetName)
	return c.AggregateID()
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

// seedCampaignConfiguration overwrites a Campaign's own configuration column directly — the
// Processor never touches the Campaign aggregate or its own event stream, so building a real
// ConfigurationChanged event here would test more than this package owns, matching
// seedCampaignAndRuleset's own read-model-seeding shape.
func seedCampaignConfiguration(t *testing.T, pool *pgxpool.Pool, campaignID uuid.UUID, configuration string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE campaigns_read_model SET configuration = $2 WHERE id = $1`, campaignID, configuration,
	); err != nil {
		t.Fatalf("seed campaign configuration: %v", err)
	}
}

// seedCharacterInfo loads the real aggregate and calls SetInfo/Save through the event store — the
// Processor's own p.characters.Load reads through postgres.Store, not characters_read_model, so
// seeding characters_read_model alone (as createCharacter's own INSERT does) would not be visible
// to tryAddTrait's/mutateInfo's Load call.
func seedCharacterInfo(t *testing.T, pool *pgxpool.Pool, characterID uuid.UUID, info string) {
	t.Helper()
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	ctx := context.Background()
	c, err := repo.Load(ctx, characterID)
	if err != nil {
		t.Fatalf("load character: %v", err)
	}
	if err := c.SetInfo(info); err != nil {
		t.Fatalf("set info: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save character info: %v", err)
	}
}

// newTestLogger returns a slog.Logger that writes to an in-memory buffer, for tests asserting a
// rejection was actually logged (not just "no mutation happened").
func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
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

	p := timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache(), discardLogger())
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

func TestCharacterProcessor_MatchingRuleset_AppendsTimestamp(t *testing.T) {
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

func TestCharacterProcessor_NonMatchingRuleset_NoOp(t *testing.T) {
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

// TestCharacterProcessor_ArchivedCharacter_NoOp covers final-review finding 1: a Character archived
// between its PUT .../action request and this engine processing the resulting
// ActionRequested must be a clean no-op, not a permanent dead-letter — retrying can't
// un-archive the aggregate, so Handle must swallow character.ErrArchived rather than fail.
func TestCharacterProcessor_ArchivedCharacter_NoOp(t *testing.T) {
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

// TestCharacterProcessor_PreservesCorrelationID covers final-review finding 2: the derived
// InfoChanged event must carry forward the correlation id from the originating
// ActionRequested envelope, not lose it to the Router's own (correlation-less) context.
func TestCharacterProcessor_PreservesCorrelationID(t *testing.T) {
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

func TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStats(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "TIMADORUS", "") // exact-case mismatch on purpose
	characterID := uuid.New()

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
	characterID = c.AggregateID()
	if err := repo.Save(context.Background(), c); err != nil {
		t.Fatalf("save character: %v", err)
	}

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   characterID,
		AggregateType: events.AggregateType,
		Version:       1,
		EventType:     events.TypeCharacterCreated,
		Payload: mustMarshal(t, events.CharacterCreated{
			ID: characterID, Name: "Elminster", CampaignID: campaignID, EntityID: uuid.New(),
			PlayerUserID: uuid.New(), OccurredAt: time.Now().UTC(),
		}),
	})

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
		Stats struct {
			TraitPoints int            `json:"traitPoints"`
			Traits      []string       `json:"traits"`
			Attributes  map[string]any `json:"attributes"`
			StatBudget  *float64       `json:"statBudget"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(infoEvent.Info), &decoded); err != nil {
		t.Fatalf("info %q is not the expected shape: %v", infoEvent.Info, err)
	}
	if decoded.Stats.TraitPoints != 2 {
		t.Fatalf("got traitPoints %d, want 2", decoded.Stats.TraitPoints)
	}
	if len(decoded.Stats.Traits) != 0 {
		t.Fatalf("got traits %v, want none", decoded.Stats.Traits)
	}
	wantAbbreviations := []string{"ST", "AG", "CO", "QU", "SD", "ME", "RE", "EM", "PR", "IN"}
	if len(decoded.Stats.Attributes) != len(wantAbbreviations) {
		t.Fatalf("got %d attributes, want %d: %v", len(decoded.Stats.Attributes), len(wantAbbreviations), decoded.Stats.Attributes)
	}
	for _, abbr := range wantAbbreviations {
		raw, ok := decoded.Stats.Attributes[abbr]
		if !ok {
			t.Fatalf("missing attribute %q", abbr)
		}
		attr, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("attribute %q is not an object: %v", abbr, raw)
		}
		if attr["temp"] != float64(50) {
			t.Fatalf("attribute %q temp = %v, want 50", abbr, attr["temp"])
		}
		if attr["pot"] != float64(50) {
			t.Fatalf("attribute %q pot = %v, want 50", abbr, attr["pot"])
		}
		if attr["bonus"] != float64(0) {
			t.Fatalf("attribute %q bonus = %v, want 0", abbr, attr["bonus"])
		}
	}
	if decoded.Stats.StatBudget != nil {
		t.Fatalf("got statBudget %v, want none (Campaign has no characterCreation configured)", *decoded.Stats.StatBudget)
	}

	wait()
}

func TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStatBudgetFromCampaign(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus", `{"characterCreation":{"maxStatBudget":40}}`)

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
	characterID := c.AggregateID()
	if err := repo.Save(context.Background(), c); err != nil {
		t.Fatalf("save character: %v", err)
	}

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   characterID,
		AggregateType: events.AggregateType,
		Version:       1,
		EventType:     events.TypeCharacterCreated,
		Payload: mustMarshal(t, events.CharacterCreated{
			ID: characterID, Name: "Elminster", CampaignID: campaignID, EntityID: uuid.New(),
			PlayerUserID: uuid.New(), OccurredAt: time.Now().UTC(),
		}),
	})

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
		Stats struct {
			StatBudget *float64 `json:"statBudget"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(infoEvent.Info), &decoded); err != nil {
		t.Fatalf("info %q is not the expected shape: %v", infoEvent.Info, err)
	}
	if decoded.Stats.StatBudget == nil {
		t.Fatal("got no statBudget, want 40")
	}
	if *decoded.Stats.StatBudget != 40 {
		t.Fatalf("got statBudget %v, want 40", *decoded.Stats.StatBudget)
	}

	wait()
}

func TestCharacterProcessor_CharacterCreated_NonMatchingRuleset_NoOp(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "SomethingElse")

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
	characterID := c.AggregateID()
	if err := repo.Save(context.Background(), c); err != nil {
		t.Fatalf("save character: %v", err)
	}

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 1,
		EventType: events.TypeCharacterCreated,
		Payload: mustMarshal(t, events.CharacterCreated{
			ID: characterID, Name: "Elminster", CampaignID: campaignID, EntityID: uuid.New(),
			PlayerUserID: uuid.New(), OccurredAt: time.Now().UTC(),
		}),
	})

	time.Sleep(500 * time.Millisecond)

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

func TestCharacterProcessor_AddTrait_ValidTrait_Succeeds(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	seedCampaignConfiguration(t, pool, campaignID, `{"traits":["strong","agile","quick"]}`)
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, `{"stats":{"traitPoints":2,"traits":[]}}`)

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload: mustMarshal(t, events.ActionRequested{
			Payload: `{"action":"addTrait","trait":"strong"}`, OccurredAt: time.Now().UTC(),
		}),
	})

	// createCharacter (version 1) + seedCharacterInfo's own SetInfo (version 2) already leave one
	// character.info_changed.v1 event in place before the router ever runs, so a plain "does any
	// row exist" poll would trivially match that seeded baseline instead of waiting for the
	// processor's own mutation — require version > 2 so this genuinely waits for a NEW event.
	deadline := time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2 AND version > 2
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
		Stats struct {
			TraitPoints int      `json:"traitPoints"`
			Traits      []string `json:"traits"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(infoEvent.Info), &decoded); err != nil {
		t.Fatalf("info %q is not the expected shape: %v", infoEvent.Info, err)
	}
	if decoded.Stats.TraitPoints != 1 {
		t.Fatalf("got traitPoints %d, want 1", decoded.Stats.TraitPoints)
	}
	if len(decoded.Stats.Traits) != 1 || decoded.Stats.Traits[0] != "strong" {
		t.Fatalf("got traits %v, want [strong]", decoded.Stats.Traits)
	}

	wait()
}

func TestCharacterProcessor_AddTrait_NotACampaignTrait_RejectedAndLogged(t *testing.T) {
	pool := newTestPool(t)
	logger, logs := newTestLogger()

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	seedCampaignConfiguration(t, pool, campaignID, `{"traits":["strong","agile","quick"]}`)
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, `{"stats":{"traitPoints":2,"traits":[]}}`)

	p := timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	body := mustMarshal(t, bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload: mustMarshal(t, events.ActionRequested{
			Payload: `{"action":"addTrait","trait":"not-a-real-trait"}`, OccurredAt: time.Now().UTC(),
		}),
	})
	msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
	if err := inMemory.Publish(bus.Subject(events.AggregateType), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// seedCharacterInfo's own SetInfo already leaves exactly one character.info_changed.v1 event
	// in place before this publish, so "no additional mutation happened" means the count stays at
	// that baseline of 1, not 0.
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d character.info_changed.v1 events, want 1 (trait is not in the Campaign's list, so no additional mutation)", count)
	}
	if !strings.Contains(logs.String(), "not-a-real-trait") {
		t.Fatalf("expected a log line naming the rejected trait, got: %s", logs.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

func TestCharacterProcessor_AddTrait_NoPointsRemaining_RejectedAndLogged(t *testing.T) {
	pool := newTestPool(t)
	logger, logs := newTestLogger()

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	seedCampaignConfiguration(t, pool, campaignID, `{"traits":["strong","agile","quick"]}`)
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, `{"stats":{"traitPoints":0,"traits":[]}}`)

	p := timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	body := mustMarshal(t, bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload: mustMarshal(t, events.ActionRequested{
			Payload: `{"action":"addTrait","trait":"strong"}`, OccurredAt: time.Now().UTC(),
		}),
	})
	msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
	if err := inMemory.Publish(bus.Subject(events.AggregateType), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// seedCharacterInfo's own SetInfo already leaves exactly one character.info_changed.v1 event
	// in place before this publish, so "no additional mutation happened" means the count stays at
	// that baseline of 1, not 0.
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d character.info_changed.v1 events, want 1 (no traitPoints remaining, so no additional mutation)", count)
	}
	if !strings.Contains(logs.String(), "traitPoints") {
		t.Fatalf("expected a log line naming the rejection reason, got: %s", logs.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

func TestCharacterProcessor_AddTrait_AlreadyHasTrait_RejectedAndLogged(t *testing.T) {
	pool := newTestPool(t)
	logger, logs := newTestLogger()

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	seedCampaignConfiguration(t, pool, campaignID, `{"traits":["strong","agile","quick"]}`)
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, `{"stats":{"traitPoints":2,"traits":["strong"]}}`)

	p := timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	body := mustMarshal(t, bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload: mustMarshal(t, events.ActionRequested{
			Payload: `{"action":"addTrait","trait":"strong"}`, OccurredAt: time.Now().UTC(),
		}),
	})
	msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
	if err := inMemory.Publish(bus.Subject(events.AggregateType), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// seedCharacterInfo's own SetInfo already leaves exactly one character.info_changed.v1 event
	// in place before this publish, so "no additional mutation happened" means the count stays at
	// that baseline of 1, not 0.
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d character.info_changed.v1 events, want 1 (already has this trait, so no additional mutation)", count)
	}
	if !strings.Contains(logs.String(), "already has") {
		t.Fatalf("expected a log line naming the rejection reason, got: %s", logs.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

// TestCharacterProcessor_AddTrait_Archived_RejectedAndLogged covers final-review fix 3: unlike
// the other three tryAddTrait rejection paths above, the character.ErrArchived race (the
// Character gets archived between its PUT .../action request and this engine processing the
// resulting ActionRequested) used to be a silent return nil — this proves it now logs too,
// exactly like the other rejection paths in this function.
func TestCharacterProcessor_AddTrait_Archived_RejectedAndLogged(t *testing.T) {
	pool := newTestPool(t)
	logger, logs := newTestLogger()

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	seedCampaignConfiguration(t, pool, campaignID, `{"traits":["strong","agile","quick"]}`)
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, `{"stats":{"traitPoints":2,"traits":[]}}`)
	archiveCharacter(t, pool, characterID)

	p := timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	body := mustMarshal(t, bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload: mustMarshal(t, events.ActionRequested{
			Payload: `{"action":"addTrait","trait":"strong"}`, OccurredAt: time.Now().UTC(),
		}),
	})
	msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
	if err := inMemory.Publish(bus.Subject(events.AggregateType), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// seedCharacterInfo's own SetInfo already leaves exactly one character.info_changed.v1 event
	// in place before this publish, so "no additional mutation happened" means the count stays at
	// that baseline of 1, not 0.
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d character.info_changed.v1 events, want 1 (character is archived, so no additional mutation)", count)
	}
	if !strings.Contains(logs.String(), "archived") {
		t.Fatalf("expected a log line naming the Character as archived, got: %s", logs.String())
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

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

// TestCharacterProcessor_SubmitPot_ValidBatch_Succeeds covers the happy path from
// docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md: a batch with one increased
// attribute (ST: 50->60, costing 10) and one unchanged attribute (AG: 50->50, costing 0) is
// applied atomically, decrementing statBudget by the total cost and leaving every other field
// (including AG's own pot) untouched.
func TestCharacterProcessor_SubmitPot_ValidBatch_Succeeds(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID,
		`{"stats":{"traitPoints":2,"traits":[],"attributes":{"ST":{"temp":50,"pot":50,"bonus":0},"AG":{"temp":50,"pot":50,"bonus":0}},"statBudget":40}}`)

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload: mustMarshal(t, events.ActionRequested{
			Payload: `{"action":"submitPot","pot":{"ST":60,"AG":50}}`, OccurredAt: time.Now().UTC(),
		}),
	})

	// seedCharacterInfo's own SetInfo (version 2) already leaves one character.info_changed.v1
	// event in place before the router ever runs — require version > 2, matching
	// TestCharacterProcessor_AddTrait_ValidTrait_Succeeds's own identical reasoning.
	deadline := time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2 AND version > 2
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
		Stats struct {
			StatBudget int `json:"statBudget"`
			Attributes map[string]struct {
				Pot int `json:"pot"`
			} `json:"attributes"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(infoEvent.Info), &decoded); err != nil {
		t.Fatalf("info %q is not the expected shape: %v", infoEvent.Info, err)
	}
	if decoded.Stats.StatBudget != 30 {
		t.Fatalf("got statBudget %d, want 30 (40 - potCost(50,60)=10)", decoded.Stats.StatBudget)
	}
	if decoded.Stats.Attributes["ST"].Pot != 60 {
		t.Fatalf("got ST pot %d, want 60", decoded.Stats.Attributes["ST"].Pot)
	}
	if decoded.Stats.Attributes["AG"].Pot != 50 {
		t.Fatalf("got AG pot %d, want 50 (unchanged)", decoded.Stats.Attributes["AG"].Pot)
	}

	wait()
}

// submitPotRejectionCase is shared by every TestCharacterProcessor_SubmitPot_*_RejectedAndLogged
// test below: seed a Character with seededInfo, publish the given submitPot payload, then assert
// no additional character.info_changed.v1 event was appended (the seeded info's own SetInfo
// already leaves exactly one) and that logs contains wantLogSubstring.
func runSubmitPotRejectionCase(t *testing.T, seededInfo, payload, wantLogSubstring string) {
	t.Helper()
	pool := newTestPool(t)
	logger, logs := newTestLogger()

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, seededInfo)

	p := timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	body := mustMarshal(t, bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload:   mustMarshal(t, events.ActionRequested{Payload: payload, OccurredAt: time.Now().UTC()}),
	})
	msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
	if err := inMemory.Publish(bus.Subject(events.AggregateType), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d character.info_changed.v1 events, want 1 (rejected, so no additional mutation)", count)
	}
	if !strings.Contains(logs.String(), wantLogSubstring) {
		t.Fatalf("expected a log line containing %q, got: %s", wantLogSubstring, logs.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

func TestCharacterProcessor_SubmitPot_InsufficientBudget_RejectedAndLogged(t *testing.T) {
	// ST 50->60 costs 10, but statBudget is only 5 — the whole batch is rejected, not partially
	// applied.
	runSubmitPotRejectionCase(t,
		`{"stats":{"attributes":{"ST":{"temp":50,"pot":50,"bonus":0}},"statBudget":5}}`,
		`{"action":"submitPot","pot":{"ST":60}}`,
		"exceeds remaining statBudget",
	)
}

func TestCharacterProcessor_SubmitPot_Decrease_RejectedAndLogged(t *testing.T) {
	runSubmitPotRejectionCase(t,
		`{"stats":{"attributes":{"ST":{"temp":50,"pot":50,"bonus":0}},"statBudget":40}}`,
		`{"action":"submitPot","pot":{"ST":40}}`,
		"below",
	)
}

func TestCharacterProcessor_SubmitPot_Over100_RejectedAndLogged(t *testing.T) {
	runSubmitPotRejectionCase(t,
		`{"stats":{"attributes":{"ST":{"temp":50,"pot":50,"bonus":0}},"statBudget":9999}}`,
		`{"action":"submitPot","pot":{"ST":101}}`,
		"100",
	)
}

func TestCharacterProcessor_SubmitPot_UnknownAbbreviation_RejectedAndLogged(t *testing.T) {
	runSubmitPotRejectionCase(t,
		`{"stats":{"attributes":{"ST":{"temp":50,"pot":50,"bonus":0}},"statBudget":40}}`,
		`{"action":"submitPot","pot":{"ZZ":60}}`,
		"unknown",
	)
}

func TestCharacterProcessor_SubmitPot_NoStatBudgetSeeded_RejectedAndLogged(t *testing.T) {
	// A non-Timadorus Character, or one the engine hasn't finished seeding yet — no
	// stats.statBudget at all, matching tryAddTrait's own "no traitPoints field" treatment.
	runSubmitPotRejectionCase(t,
		`{"stats":{"attributes":{"ST":{"temp":50,"pot":50,"bonus":0}}}}`,
		`{"action":"submitPot","pot":{"ST":60}}`,
		"has no statBudget",
	)
}
