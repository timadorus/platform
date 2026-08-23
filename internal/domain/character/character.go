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
	info         string
	archived     bool
}

func (c *Character) Name() string            { return c.name }
func (c *Character) CampaignID() uuid.UUID   { return c.campaignID }
func (c *Character) EntityID() uuid.UUID     { return c.entityID }
func (c *Character) PlayerUserID() uuid.UUID { return c.playerUserID }

// Info is an opaque JSON-string payload (the backend never parses or validates it) — see
// SetInfo for the only command that changes it.
func (c *Character) Info() string     { return c.info }
func (c *Character) IsArchived() bool { return c.archived }

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

// SetInfo replaces the Character's opaque info string wholesale (same "replace, don't merge"
// shape as ruleset.Ruleset.SetDescription/SetReferences — no minimum-content invariant to
// protect, so unlike Rename/SetPlayer there's no dedupe-if-unchanged short-circuit).
func (c *Character) SetInfo(info string) error {
	if c.archived {
		return ErrArchived
	}
	c.raise(&events.InfoChanged{Info: info, OccurredAt: time.Now().UTC()})
	return nil
}

// RequestAction raises ActionRequested without mutating any aggregate field (see Apply below) —
// the actual effect, if any, happens later and asynchronously in timadorus-engine, and only
// conditionally on the Character's Campaign's Ruleset (see that package's own docs). Guarded by
// the same archived check every other mutating command uses, since a request against an
// archived Character shouldn't be accepted even though it has no direct effect here.
func (c *Character) RequestAction(payload string) error {
	if c.archived {
		return ErrArchived
	}
	c.raise(&events.ActionRequested{Payload: payload, OccurredAt: time.Now().UTC()})
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
		c.info = e.Info
	case *events.CharacterRenamed:
		c.name = e.Name
	case *events.PlayerChanged:
		c.playerUserID = e.NewPlayerUserID
	case *events.InfoChanged:
		c.info = e.Info
	case *events.ActionRequested:
		// Intentionally a no-op — see RequestAction's doc comment. An explicit case (rather
		// than falling through to no case at all) keeps this switch exhaustive and
		// self-documenting: a future reader shouldn't have to wonder whether it was forgotten.
	case *events.CharacterArchived:
		c.archived = true
	}
}

func (c *Character) raise(event eventsourcing.Event) {
	c.Base.Raise(c, event)
}
