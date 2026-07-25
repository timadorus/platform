// Package events holds Entity's domain events and their registry hookup only — no business
// logic (same shape as domain/universe/events; see that package's doc comment).
package events

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType is the stable string used to namespace this aggregate's events in the event
// store and its subject on the event bus.
const AggregateType = "entity"

const (
	TypeEntityCreated  = "entity.created.v1"
	TypeEntityRenamed  = "entity.renamed.v1"
	TypeEntityArchived = "entity.archived.v1"
)

// EntityCreated carries UniverseID (the immutable parent reference) since that isn't
// derivable from the envelope (plan §4.5).
type EntityCreated struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	UniverseID uuid.UUID `json:"universeId"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (EntityCreated) EventType() string { return TypeEntityCreated }

type EntityRenamed struct {
	Name       string    `json:"name"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (EntityRenamed) EventType() string { return TypeEntityRenamed }

type EntityArchived struct {
	OccurredAt time.Time `json:"occurredAt"`
}

func (EntityArchived) EventType() string { return TypeEntityArchived }

// Register hooks every Entity event into reg so infrastructure can deserialize persisted
// payloads back into these concrete types during replay.
func Register(reg *eventsourcing.Registry) {
	reg.Register(TypeEntityCreated, func() eventsourcing.Event { return &EntityCreated{} })
	reg.Register(TypeEntityRenamed, func() eventsourcing.Event { return &EntityRenamed{} })
	reg.Register(TypeEntityArchived, func() eventsourcing.Event { return &EntityArchived{} })
}
