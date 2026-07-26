// Package campaign is the second reference aggregate: it repeats Universe's
// collection-invariant and archiving pattern for Gamemasters, and adds an immutable parent
// reference (UniverseID) — the shape every other parented aggregate (Entity, Object,
// Character) follows.
package campaign

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/campaign/events"
	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType re-exports events.AggregateType — see domain/universe.AggregateType for why.
const AggregateType = events.AggregateType

type Campaign struct {
	eventsourcing.Base

	name        string
	universeID  uuid.UUID
	rulesetID   uuid.UUID
	gamemasters map[uuid.UUID]struct{}
	archived    bool
}

func (c *Campaign) Name() string          { return c.name }
func (c *Campaign) UniverseID() uuid.UUID { return c.universeID }
func (c *Campaign) RulesetID() uuid.UUID  { return c.rulesetID }
func (c *Campaign) IsArchived() bool      { return c.archived }
func (c *Campaign) HasGamemaster(id uuid.UUID) bool {
	_, ok := c.gamemasters[id]
	return ok
}

// New constructs and creates a new Campaign under universeID, referencing rulesetID
// immutably (plan §2 — a Campaign's Ruleset can never change; a new Campaign must be created
// to use a different one). The caller (the application command service) is responsible for
// having already validated that universeID and rulesetID refer to existing, non-archived
// aggregates and that every id in gamemasterIDs refers to an existing, non-archived User
// (plan §4.3) — this constructor only enforces the aggregate's own invariants (non-blank
// name, non-empty Gamemasters).
func New(universeID, rulesetID uuid.UUID, name string, gamemasterIDs []uuid.UUID) (*Campaign, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	ids := dedupe(gamemasterIDs)
	if len(ids) == 0 {
		return nil, ErrGamemastersRequired
	}
	c := &Campaign{}
	c.raise(&events.CampaignCreated{
		ID:                uuid.New(),
		Name:              name,
		UniverseID:        universeID,
		RulesetID:         rulesetID,
		GamemasterUserIDs: ids,
		OccurredAt:        time.Now().UTC(),
	})
	return c, nil
}

func (c *Campaign) Rename(name string) error {
	if c.archived {
		return ErrArchived
	}
	if name == "" {
		return ErrNameRequired
	}
	if name == c.name {
		return nil
	}
	c.raise(&events.CampaignRenamed{Name: name, OccurredAt: time.Now().UTC()})
	return nil
}

func (c *Campaign) AddGamemaster(userID uuid.UUID) error {
	if c.archived {
		return ErrArchived
	}
	if c.HasGamemaster(userID) {
		return nil
	}
	c.raise(&events.GamemasterAdded{UserID: userID, OccurredAt: time.Now().UTC()})
	return nil
}

func (c *Campaign) RemoveGamemaster(userID uuid.UUID) error {
	if c.archived {
		return ErrArchived
	}
	if !c.HasGamemaster(userID) {
		return ErrGamemasterNotFound
	}
	if len(c.gamemasters) == 1 {
		return ErrLastGamemaster
	}
	c.raise(&events.GamemasterRemoved{UserID: userID, OccurredAt: time.Now().UTC()})
	return nil
}

// Archive is idempotent — see universe.Universe.Archive's doc comment for why.
func (c *Campaign) Archive() error {
	if c.archived {
		return nil
	}
	c.raise(&events.CampaignArchived{OccurredAt: time.Now().UTC()})
	return nil
}

func (c *Campaign) Apply(event eventsourcing.Event) {
	switch e := event.(type) {
	case *events.CampaignCreated:
		c.SetID(e.ID)
		c.name = e.Name
		c.universeID = e.UniverseID
		c.rulesetID = e.RulesetID
		c.gamemasters = toSet(e.GamemasterUserIDs)
	case *events.CampaignRenamed:
		c.name = e.Name
	case *events.GamemasterAdded:
		c.gamemasters[e.UserID] = struct{}{}
	case *events.GamemasterRemoved:
		delete(c.gamemasters, e.UserID)
	case *events.CampaignArchived:
		c.archived = true
	}
}

func (c *Campaign) raise(event eventsourcing.Event) {
	c.Base.Raise(c, event)
}

func dedupe(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func toSet(ids []uuid.UUID) map[uuid.UUID]struct{} {
	set := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}
