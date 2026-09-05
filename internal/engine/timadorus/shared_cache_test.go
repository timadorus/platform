package timadorus_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign/events"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/projection"
)

// TestSharedRulesetCache_ServesBothProcessors proves the design's central claim: one
// RulesetCache instance, populated by a Campaign-triggered lookup, is then used as-is by a
// Character-triggered lookup for the same campaign — without a second query — even after the
// underlying ruleset name changes in Postgres. This exercises both processors together, wired
// exactly as cmd/timadorus-engine/main.go wires them. It does NOT exercise genuine concurrent
// access to the cache (the two publishes are strictly sequenced: publish, wait for completion,
// then publish again) — see TestSharedRulesetCache_ConcurrentAccess below for that.
func TestSharedRulesetCache_ServesBothProcessors(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)

	cache := timadorus.NewRulesetCache()
	campaignProcessor := timadorus.NewCampaignProcessor(pool, cache)
	characterProcessor := timadorus.NewCharacterProcessor(pool, cache, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{campaignProcessor, characterProcessor}) }()

	publish := func(subject string, env bus.Envelope) {
		body := mustMarshal(t, env)
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(subject, msg); err != nil {
			t.Fatalf("publish to %s: %v", subject, err)
		}
	}

	// First: a Campaign-triggered lookup populates the shared cache with "timadorus".
	publish(bus.Subject(events.AggregateType), bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeConfigurationRequested,
		Payload:   mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
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
		Payload:   mustMarshal(t, characterevents.ActionRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
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

// TestSharedRulesetCache_ConcurrentAccess fires a Campaign-triggered lookup and a
// Character-triggered lookup for the SAME campaign at the same time — released together via a
// shared start signal, not sequenced with a wait in between like
// TestSharedRulesetCache_ServesBothProcessors above — so CampaignProcessor.Handle and
// CharacterProcessor.Handle genuinely race to call RulesetCache.resolve concurrently. Run this
// package's tests with `go test -race` for this to actually catch a data race in the cache's
// locking; this test's own job is only to create real concurrent access for -race to have
// something to check (TestSharedRulesetCache_ServesBothProcessors already covers the end-state
// assertion that both processors end up using one shared cache entry).
func TestSharedRulesetCache_ConcurrentAccess(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)
	characterID := createCharacter(t, pool, campaignID)

	cache := timadorus.NewRulesetCache()
	campaignProcessor := timadorus.NewCampaignProcessor(pool, cache)
	characterProcessor := timadorus.NewCharacterProcessor(pool, cache, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{campaignProcessor, characterProcessor}) }()

	campaignEnvelope := bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeConfigurationRequested,
		Payload:   mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	}
	characterEnvelope := bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: characterevents.AggregateType, Version: 2,
		EventType: characterevents.TypeActionRequested,
		Payload:   mustMarshal(t, characterevents.ActionRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	}

	// Both goroutines block on the same closed-once start channel and are released together, so
	// their two inMemory.Publish calls (and the two Handle invocations they trigger on separate
	// consumer goroutines) genuinely race to call cache.resolve for the same campaign id, rather
	// than being ordered by test code the way TestSharedRulesetCache_ServesBothProcessors is.
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		body := mustMarshal(t, campaignEnvelope)
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(bus.Subject(events.AggregateType), msg); err != nil {
			t.Errorf("publish campaign envelope: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		body := mustMarshal(t, characterEnvelope)
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(bus.Subject(characterevents.AggregateType), msg); err != nil {
			t.Errorf("publish character envelope: %v", err)
		}
	}()
	close(start)
	wg.Wait()

	waitForConfigurationChanged(t, pool, campaignID)
	waitForInfoChanged(t, pool, characterID)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

func waitForInfoChanged(t *testing.T, pool *pgxpool.Pool, characterID uuid.UUID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
			characterID, characterevents.TypeInfoChanged,
		).Scan(&count); err != nil {
			t.Fatalf("poll info_changed: %v", err)
		}
		if count == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for character.info_changed.v1")
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
