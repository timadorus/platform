package universechanges_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	entityevents "github.com/timadorus/platform/internal/domain/entity/events"
	objectevents "github.com/timadorus/platform/internal/domain/object/events"
	universeevents "github.com/timadorus/platform/internal/domain/universe/events"
	"github.com/timadorus/platform/internal/projection"
	"github.com/timadorus/platform/internal/projection/universechanges"
)

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// runProjector wires one Projector to its own in-memory subscriber, running until wait() is
// called — mirrors internal/engine/timadorus's runCampaignEngine/runCampaignEngine-shaped
// helpers, adapted for a plain projection.Projector instead of an engine processor.
func runProjector(t *testing.T, pool *pgxpool.Pool, p projection.Projector, subject string) (publish func(env bus.Envelope), wait func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })

	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())

	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

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

func waitForChangeRow(t *testing.T, pool *pgxpool.Pool, globalSeq int64) (universeID uuid.UUID, found bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT universe_id FROM universe_changes_read_model WHERE global_seq = $1`, globalSeq,
		).Scan(&universeID)
		if err == nil {
			return universeID, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return uuid.Nil, false
}

func TestUniverseProjector_InsertsRow(t *testing.T) {
	pool := newTestPool(t)
	p := universechanges.NewUniverseProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(universeevents.AggregateType))

	universeID := uuid.New()
	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: universeID, AggregateType: universeevents.AggregateType, Version: 1,
		EventType: universeevents.TypeUniverseCreated,
		Payload:   mustMarshal(t, universeevents.UniverseCreated{ID: universeID, Name: "Test", CreatorUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})

	got, found := waitForChangeRow(t, pool, 1)
	if !found {
		t.Fatal("timed out waiting for the change row")
	}
	if got != universeID {
		t.Fatalf("got universe_id %s, want %s", got, universeID)
	}
	wait()
}

func TestCampaignProjector_Created_UsesPayloadUniverseID(t *testing.T) {
	pool := newTestPool(t)
	p := universechanges.NewCampaignProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(campaignevents.AggregateType))

	campaignID, universeID := uuid.New(), uuid.New()
	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: campaignevents.AggregateType, Version: 1,
		EventType: campaignevents.TypeCampaignCreated,
		Payload: mustMarshal(t, campaignevents.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: universeID, RulesetID: uuid.New(),
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC(),
		}),
		CreatedAt: time.Now().UTC(),
	})

	got, found := waitForChangeRow(t, pool, 1)
	if !found {
		t.Fatal("timed out waiting for the change row")
	}
	if got != universeID {
		t.Fatalf("got universe_id %s, want %s (from the event payload, no read-model lookup needed)", got, universeID)
	}
	wait()
}

func TestCampaignProjector_Renamed_ResolvesViaReadModel(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID := uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, configuration, is_archived, updated_at)
		 VALUES ($1, 'Test Campaign', $2, $3, '', false, now())`,
		campaignID, universeID, uuid.New(),
	); err != nil {
		t.Fatalf("seed campaigns_read_model: %v", err)
	}

	p := universechanges.NewCampaignProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(campaignevents.AggregateType))

	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: campaignevents.AggregateType, Version: 2,
		EventType: campaignevents.TypeCampaignRenamed,
		Payload:   mustMarshal(t, campaignevents.CampaignRenamed{Name: "Renamed", OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})

	got, found := waitForChangeRow(t, pool, 1)
	if !found {
		t.Fatal("timed out waiting for the change row")
	}
	if got != universeID {
		t.Fatalf("got universe_id %s, want %s (resolved via campaigns_read_model)", got, universeID)
	}
	wait()
}

func TestEntityProjector_CreatedAndRenamed(t *testing.T) {
	pool := newTestPool(t)
	p := universechanges.NewEntityProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(entityevents.AggregateType))

	entityID, universeID := uuid.New(), uuid.New()
	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: entityID, AggregateType: entityevents.AggregateType, Version: 1,
		EventType: entityevents.TypeEntityCreated,
		Payload:   mustMarshal(t, entityevents.EntityCreated{ID: entityID, Name: "Test Entity", UniverseID: universeID, OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})
	if got, found := waitForChangeRow(t, pool, 1); !found || got != universeID {
		t.Fatalf("created: got %s, found=%v, want %s", got, found, universeID)
	}

	if _, err := pool.Exec(context.Background(),
		`INSERT INTO entities_read_model (id, name, universe_id, is_archived, updated_at) VALUES ($1, 'Test Entity', $2, false, now())`,
		entityID, universeID,
	); err != nil {
		t.Fatalf("seed entities_read_model: %v", err)
	}
	publish(bus.Envelope{
		GlobalSeq: 2, AggregateID: entityID, AggregateType: entityevents.AggregateType, Version: 2,
		EventType: entityevents.TypeEntityRenamed,
		Payload:   mustMarshal(t, entityevents.EntityRenamed{Name: "Renamed Entity", OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})
	if got, found := waitForChangeRow(t, pool, 2); !found || got != universeID {
		t.Fatalf("renamed: got %s, found=%v, want %s", got, found, universeID)
	}
	wait()
}

func TestObjectProjector_CreatedAndRenamed(t *testing.T) {
	pool := newTestPool(t)
	p := universechanges.NewObjectProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(objectevents.AggregateType))

	objectID, universeID := uuid.New(), uuid.New()
	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: objectID, AggregateType: objectevents.AggregateType, Version: 1,
		EventType: objectevents.TypeObjectCreated,
		Payload:   mustMarshal(t, objectevents.ObjectCreated{ID: objectID, Name: "Test Object", UniverseID: universeID, OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})
	if got, found := waitForChangeRow(t, pool, 1); !found || got != universeID {
		t.Fatalf("created: got %s, found=%v, want %s", got, found, universeID)
	}

	if _, err := pool.Exec(context.Background(),
		`INSERT INTO objects_read_model (id, name, universe_id, is_archived, updated_at) VALUES ($1, 'Test Object', $2, false, now())`,
		objectID, universeID,
	); err != nil {
		t.Fatalf("seed objects_read_model: %v", err)
	}
	publish(bus.Envelope{
		GlobalSeq: 2, AggregateID: objectID, AggregateType: objectevents.AggregateType, Version: 2,
		EventType: objectevents.TypeObjectRenamed,
		Payload:   mustMarshal(t, objectevents.ObjectRenamed{Name: "Renamed Object", OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})
	if got, found := waitForChangeRow(t, pool, 2); !found || got != universeID {
		t.Fatalf("renamed: got %s, found=%v, want %s", got, found, universeID)
	}
	wait()
}

func TestCharacterProjector_Created_ResolvesViaCampaign(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID := uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, configuration, is_archived, updated_at)
		 VALUES ($1, 'Test Campaign', $2, $3, '', false, now())`,
		campaignID, universeID, uuid.New(),
	); err != nil {
		t.Fatalf("seed campaigns_read_model: %v", err)
	}

	p := universechanges.NewCharacterProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(characterevents.AggregateType))

	characterID, entityID, playerID := uuid.New(), uuid.New(), uuid.New()
	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: characterevents.AggregateType, Version: 1,
		EventType: characterevents.TypeCharacterCreated,
		Payload: mustMarshal(t, characterevents.CharacterCreated{
			ID: characterID, Name: "Aragorn", CampaignID: campaignID, EntityID: entityID,
			PlayerUserID: playerID, Info: "", OccurredAt: time.Now().UTC(),
		}),
		CreatedAt: time.Now().UTC(),
	})

	got, found := waitForChangeRow(t, pool, 1)
	if !found {
		t.Fatal("timed out waiting for the change row")
	}
	if got != universeID {
		t.Fatalf("got universe_id %s, want %s (resolved via the payload's CampaignID, then campaigns_read_model)", got, universeID)
	}
	wait()
}

func TestCharacterProjector_Renamed_ResolvesViaJoin(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID, characterID, entityID, playerID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, configuration, is_archived, updated_at)
		 VALUES ($1, 'Test Campaign', $2, $3, '', false, now())`,
		campaignID, universeID, uuid.New(),
	); err != nil {
		t.Fatalf("seed campaigns_read_model: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO characters_read_model (id, name, campaign_id, entity_id, player_user_id, info, is_archived, updated_at)
		 VALUES ($1, 'Aragorn', $2, $3, $4, '', false, now())`,
		characterID, campaignID, entityID, playerID,
	); err != nil {
		t.Fatalf("seed characters_read_model: %v", err)
	}

	p := universechanges.NewCharacterProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(characterevents.AggregateType))

	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: characterevents.AggregateType, Version: 2,
		EventType: characterevents.TypeCharacterRenamed,
		Payload:   mustMarshal(t, characterevents.CharacterRenamed{Name: "Strider", OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})

	got, found := waitForChangeRow(t, pool, 1)
	if !found {
		t.Fatal("timed out waiting for the change row")
	}
	if got != universeID {
		t.Fatalf("got universe_id %s, want %s (resolved via the characters_read_model/campaigns_read_model join)", got, universeID)
	}
	wait()
}
