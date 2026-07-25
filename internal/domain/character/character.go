// Package character is the aggregate that exercises the platform's one cross-aggregate
// creation flow: a Character is always created together with a paired Entity (plan §4.4).
// This package only knows its own invariants (name, mandatory Player); the atomic
// two-aggregate creation itself lives in the application layer
// (internal/command/character.Service), which is where a UnitOfWork spans both aggregates'
// Repository.Save calls.
package character

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType re-exports events.AggregateType — see domain/universe.AggregateType for why.
const AggregateType = events.AggregateType

type Character struct {
	eventsourcing.Base

	name         string
	campaignID   uuid.UUID
	entityID     uuid.UUID
	playerUserID uuid.UUID
	archived     bool
}

func (c *Character) Name() string            { return c.name }
func (c *Character) CampaignID() uuid.UUID   { return c.campaignID }
func (c *Character) EntityID() uuid.UUID     { return c.entityID }
func (c *Character) PlayerUserID() uuid.UUID { return c.playerUserID }
func (c *Character) IsArchived() bool        { return c.archived }

// New constructs and creates a new Character under campaignID, paired with the Entity
// identified by entityID. The caller (internal/command/character.Service) is responsible
// for having already: validated campaignID/playerUserID reference existing, non-archived
// aggregates, and created (in the same UnitOfWork) the Entity that entityID refers to — this
// constructor only enforces the aggregate's own invariants (non-blank name, non-nil player).
func New(campaignID, entityID, playerUserID uuid.UUID, name string) (*Character, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	if playerUserID == uuid.Nil {
		return nil, ErrPlayerRequired
	}
	c := &Character{}
	c.raise(&events.CharacterCreated{
		ID:           uuid.New(),
		Name:         name,
		CampaignID:   campaignID,
		EntityID:     entityID,
		PlayerUserID: playerUserID,
		OccurredAt:   time.Now().UTC(),
	})
	return c, nil
}

func (c *Character) Rename(name string) error {
	if c.archived {
		return ErrArchived
	}
	if name == "" {
		return ErrNameRequired
	}
	if name == c.name {
		return nil
	}
	c.raise(&events.CharacterRenamed{Name: name, OccurredAt: time.Now().UTC()})
	return nil
}

// SetPlayer reassigns the Character's Player. There is no "unset" — a Character always has
// exactly one Player (plan §2) — so this rejects uuid.Nil rather than offering a remove
// operation.
func (c *Character) SetPlayer(userID uuid.UUID) error {
	if c.archived {
		return ErrArchived
	}
	if userID == uuid.Nil {
		return ErrPlayerRequired
	}
	if userID == c.playerUserID {
		return nil
	}
	c.raise(&events.PlayerChanged{NewPlayerUserID: userID, OccurredAt: time.Now().UTC()})
	return nil
}

// Archive is idempotent — see universe.Universe.Archive's doc comment for why.
func (c *Character) Archive() error {
	if c.archived {
		return nil
	}
	c.raise(&events.CharacterArchived{OccurredAt: time.Now().UTC()})
	return nil
}

func (c *Character) Apply(event eventsourcing.Event) {
	switch e := event.(type) {
	case *events.CharacterCreated:
		c.SetID(e.ID)
		c.name = e.Name
		c.campaignID = e.CampaignID
		c.entityID = e.EntityID
		c.playerUserID = e.PlayerUserID
	case *events.CharacterRenamed:
		c.name = e.Name
	case *events.PlayerChanged:
		c.playerUserID = e.NewPlayerUserID
	case *events.CharacterArchived:
		c.archived = true
	}
}

func (c *Character) raise(event eventsourcing.Event) {
	c.Base.Raise(c, event)
}
