package aggregateresolve_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/aggregateresolve"
	"github.com/timadorus/platform/internal/bus"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	entityevents "github.com/timadorus/platform/internal/domain/entity/events"
	objectevents "github.com/timadorus/platform/internal/domain/object/events"
)

func TestCampaign_Created_UsesPayloadUniverseID(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID := uuid.New(), uuid.New()

	got, err := aggregateresolve.Campaign(context.Background(), pool, bus.Envelope{
		AggregateID: campaignID, EventType: campaignevents.TypeCampaignCreated,
		Payload: mustMarshal(t, campaignevents.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: universeID, RulesetID: uuid.New(),
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC(),
		}),
	})
	if err != nil {
		t.Fatalf("Campaign: %v", err)
	}
	if got != universeID {
		t.Fatalf("got %s, want %s (from the payload, no query needed)", got, universeID)
	}
}

func TestCampaign_OtherEvent_ResolvesViaReadModel(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID := uuid.New(), uuid.New()
	seedCampaign(t, pool, campaignID, universeID)

	got, err := aggregateresolve.Campaign(context.Background(), pool, bus.Envelope{
		AggregateID: campaignID, EventType: campaignevents.TypeCampaignRenamed,
		Payload: mustMarshal(t, campaignevents.CampaignRenamed{Name: "Renamed", OccurredAt: time.Now().UTC()}),
	})
	if err != nil {
		t.Fatalf("Campaign: %v", err)
	}
	if got != universeID {
		t.Fatalf("got %s, want %s (resolved via campaigns_read_model)", got, universeID)
	}
}

func TestEntity_Created_UsesPayloadUniverseID(t *testing.T) {
	pool := newTestPool(t)
	entityID, universeID := uuid.New(), uuid.New()

	got, err := aggregateresolve.Entity(context.Background(), pool, bus.Envelope{
		AggregateID: entityID, EventType: entityevents.TypeEntityCreated,
		Payload: mustMarshal(t, entityevents.EntityCreated{ID: entityID, Name: "Test Entity", UniverseID: universeID, OccurredAt: time.Now().UTC()}),
	})
	if err != nil {
		t.Fatalf("Entity: %v", err)
	}
	if got != universeID {
		t.Fatalf("got %s, want %s", got, universeID)
	}
}

func TestEntity_OtherEvent_ResolvesViaReadModel(t *testing.T) {
	pool := newTestPool(t)
	entityID, universeID := uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO entities_read_model (id, name, universe_id, is_archived, updated_at) VALUES ($1, 'Test Entity', $2, false, now())`,
		entityID, universeID,
	); err != nil {
		t.Fatalf("seed entities_read_model: %v", err)
	}

	got, err := aggregateresolve.Entity(context.Background(), pool, bus.Envelope{
		AggregateID: entityID, EventType: entityevents.TypeEntityRenamed,
		Payload: mustMarshal(t, entityevents.EntityRenamed{Name: "Renamed", OccurredAt: time.Now().UTC()}),
	})
	if err != nil {
		t.Fatalf("Entity: %v", err)
	}
	if got != universeID {
		t.Fatalf("got %s, want %s", got, universeID)
	}
}

func TestObject_Created_UsesPayloadUniverseID(t *testing.T) {
	pool := newTestPool(t)
	objectID, universeID := uuid.New(), uuid.New()

	got, err := aggregateresolve.Object(context.Background(), pool, bus.Envelope{
		AggregateID: objectID, EventType: objectevents.TypeObjectCreated,
		Payload: mustMarshal(t, objectevents.ObjectCreated{ID: objectID, Name: "Test Object", UniverseID: universeID, OccurredAt: time.Now().UTC()}),
	})
	if err != nil {
		t.Fatalf("Object: %v", err)
	}
	if got != universeID {
		t.Fatalf("got %s, want %s", got, universeID)
	}
}

func TestObject_OtherEvent_ResolvesViaReadModel(t *testing.T) {
	pool := newTestPool(t)
	objectID, universeID := uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO objects_read_model (id, name, universe_id, is_archived, updated_at) VALUES ($1, 'Test Object', $2, false, now())`,
		objectID, universeID,
	); err != nil {
		t.Fatalf("seed objects_read_model: %v", err)
	}

	got, err := aggregateresolve.Object(context.Background(), pool, bus.Envelope{
		AggregateID: objectID, EventType: objectevents.TypeObjectRenamed,
		Payload: mustMarshal(t, objectevents.ObjectRenamed{Name: "Renamed", OccurredAt: time.Now().UTC()}),
	})
	if err != nil {
		t.Fatalf("Object: %v", err)
	}
	if got != universeID {
		t.Fatalf("got %s, want %s", got, universeID)
	}
}

func TestCharacter_Created_ResolvesUniverseViaCampaignAndReturnsCampaignID(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID := uuid.New(), uuid.New()
	seedCampaign(t, pool, campaignID, universeID)

	characterID, entityID, playerID := uuid.New(), uuid.New(), uuid.New()
	gotUniverse, gotCampaign, err := aggregateresolve.Character(context.Background(), pool, bus.Envelope{
		AggregateID: characterID, EventType: characterevents.TypeCharacterCreated,
		Payload: mustMarshal(t, characterevents.CharacterCreated{
			ID: characterID, Name: "Aragorn", CampaignID: campaignID, EntityID: entityID,
			PlayerUserID: playerID, Info: "", OccurredAt: time.Now().UTC(),
		}),
	})
	if err != nil {
		t.Fatalf("Character: %v", err)
	}
	if gotUniverse != universeID {
		t.Fatalf("got universeID %s, want %s", gotUniverse, universeID)
	}
	if gotCampaign != campaignID {
		t.Fatalf("got campaignID %s, want %s (straight from the payload, no query needed)", gotCampaign, campaignID)
	}
}

func TestCharacter_OtherEvent_ResolvesBothViaJoin(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID, characterID, entityID, playerID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedCampaign(t, pool, campaignID, universeID)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO characters_read_model (id, name, campaign_id, entity_id, player_user_id, info, is_archived, updated_at)
		 VALUES ($1, 'Aragorn', $2, $3, $4, '', false, now())`,
		characterID, campaignID, entityID, playerID,
	); err != nil {
		t.Fatalf("seed characters_read_model: %v", err)
	}

	gotUniverse, gotCampaign, err := aggregateresolve.Character(context.Background(), pool, bus.Envelope{
		AggregateID: characterID, EventType: characterevents.TypeCharacterRenamed,
		Payload: mustMarshal(t, characterevents.CharacterRenamed{Name: "Strider", OccurredAt: time.Now().UTC()}),
	})
	if err != nil {
		t.Fatalf("Character: %v", err)
	}
	if gotUniverse != universeID {
		t.Fatalf("got universeID %s, want %s", gotUniverse, universeID)
	}
	if gotCampaign != campaignID {
		t.Fatalf("got campaignID %s, want %s (resolved via the characters_read_model/campaigns_read_model join)", gotCampaign, campaignID)
	}
}
