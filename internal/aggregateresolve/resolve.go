// Package aggregateresolve resolves a Universe-scoped aggregate event's own owning Universe (and,
// for Character events, owning Campaign) id — the one piece of cross-projection lookup logic
// every Universe/Campaign/Entity/Object/Character event needs to answer "which Universe does this
// belong to," shared between internal/projection/universechanges (the write side, called with a
// transaction) and cmd/realtime (the read side, called with a plain pool — no ambient
// transaction). Universe itself needs no resolver: a Universe event's own aggregate id already is
// the Universe id.
package aggregateresolve

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	entityevents "github.com/timadorus/platform/internal/domain/entity/events"
	objectevents "github.com/timadorus/platform/internal/domain/object/events"
)

// Querier is satisfied by both pgx.Tx (the write side's ambient transaction) and *pgxpool.Pool
// (cmd/realtime's own pool, with no transaction) — mirrors the identical minimal-interface
// pattern already used for the same reason by the unexported querier in
// internal/eventstore/postgres/store.go.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Campaign resolves a Campaign event's own UniverseID: free (straight from the event's own
// payload) for CampaignCreated, a single read-model query for every other Campaign event.
func Campaign(ctx context.Context, q Querier, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == campaignevents.TypeCampaignCreated {
		var e campaignevents.CampaignCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("aggregateresolve: unmarshal %s: %w", env.EventType, err)
		}
		return e.UniverseID, nil
	}
	var universeID uuid.UUID
	if err := q.QueryRow(ctx,
		`SELECT universe_id FROM campaigns_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("aggregateresolve: resolve universe for campaign %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}

// Entity resolves an Entity event's own UniverseID — mirrors Campaign's exact shape.
func Entity(ctx context.Context, q Querier, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == entityevents.TypeEntityCreated {
		var e entityevents.EntityCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("aggregateresolve: unmarshal %s: %w", env.EventType, err)
		}
		return e.UniverseID, nil
	}
	var universeID uuid.UUID
	if err := q.QueryRow(ctx,
		`SELECT universe_id FROM entities_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("aggregateresolve: resolve universe for entity %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}

// Object resolves an Object event's own UniverseID — mirrors Campaign's exact shape.
func Object(ctx context.Context, q Querier, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == objectevents.TypeObjectCreated {
		var e objectevents.ObjectCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("aggregateresolve: unmarshal %s: %w", env.EventType, err)
		}
		return e.UniverseID, nil
	}
	var universeID uuid.UUID
	if err := q.QueryRow(ctx,
		`SELECT universe_id FROM objects_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("aggregateresolve: resolve universe for object %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}

// Character resolves a Character event's own UniverseID *and* CampaignID — the one resolver that
// needs a second return value, since nothing needed CampaignID before cmd/realtime's own
// campaign-scoped Character-list filter (see the design spec's Decision 3/4). CharacterCreated
// carries CampaignID directly in its own payload (so UniverseID needs one query, CampaignID needs
// none); every other event resolves both in a single joined query.
func Character(ctx context.Context, q Querier, env bus.Envelope) (universeID, campaignID uuid.UUID, err error) {
	if env.EventType == characterevents.TypeCharacterCreated {
		var e characterevents.CharacterCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, uuid.Nil, fmt.Errorf("aggregateresolve: unmarshal %s: %w", env.EventType, err)
		}
		if err := q.QueryRow(ctx,
			`SELECT universe_id FROM campaigns_read_model WHERE id = $1`, e.CampaignID,
		).Scan(&universeID); err != nil {
			return uuid.Nil, uuid.Nil, fmt.Errorf("aggregateresolve: resolve universe for campaign %s (character %s create): %w", e.CampaignID, env.AggregateID, err)
		}
		return universeID, e.CampaignID, nil
	}

	if err := q.QueryRow(ctx,
		`SELECT c.universe_id, ch.campaign_id FROM characters_read_model ch
		 JOIN campaigns_read_model c ON c.id = ch.campaign_id
		 WHERE ch.id = $1`, env.AggregateID,
	).Scan(&universeID, &campaignID); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("aggregateresolve: resolve universe/campaign for character %s: %w", env.AggregateID, err)
	}
	return universeID, campaignID, nil
}
