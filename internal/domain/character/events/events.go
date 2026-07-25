// Package events holds Character's domain events and their registry hookup only — no
// business logic (same shape as domain/universe/events; see that package's doc comment).
package events

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType is the stable string used to namespace this aggregate's events in the event
// store and its subject on the event bus.
const AggregateType = "character"

const (
	TypeCharacterCreated  = "character.created.v1"
	TypeCharacterRenamed  = "character.renamed.v1"
	TypePlayerChanged     = "character.player_changed.v1"
	TypeCharacterArchived = "character.archived.v1"
)

// CharacterCreated carries CampaignID (immutable parent), EntityID (the auto-created paired
// Entity — plan §4.4), and PlayerUserID, none of which are derivable from the envelope,
// which only identifies the Character itself (plan §4.5).
type CharacterCreated struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	CampaignID   uuid.UUID `json:"campaignId"`
	EntityID     uuid.UUID `json:"entityId"`
	PlayerUserID uuid.UUID `json:"playerUserId"`
	OccurredAt   time.Time `json:"occurredAt"`
}

func (CharacterCreated) EventType() string { return TypeCharacterCreated }

type CharacterRenamed struct {
	Name       string    `json:"name"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (CharacterRenamed) EventType() string { return TypeCharacterRenamed }

type PlayerChanged struct {
	NewPlayerUserID uuid.UUID `json:"newPlayerUserId"`
	OccurredAt      time.Time `json:"occurredAt"`
}

func (PlayerChanged) EventType() string { return TypePlayerChanged }

type CharacterArchived struct {
	OccurredAt time.Time `json:"occurredAt"`
}

func (CharacterArchived) EventType() string { return TypeCharacterArchived }

// Register hooks every Character event into reg so infrastructure can deserialize persisted
// payloads back into these concrete types during replay.
func Register(reg *eventsourcing.Registry) {
	reg.Register(TypeCharacterCreated, func() eventsourcing.Event { return &CharacterCreated{} })
	reg.Register(TypeCharacterRenamed, func() eventsourcing.Event { return &CharacterRenamed{} })
	reg.Register(TypePlayerChanged, func() eventsourcing.Event { return &PlayerChanged{} })
	reg.Register(TypeCharacterArchived, func() eventsourcing.Event { return &CharacterArchived{} })
}
