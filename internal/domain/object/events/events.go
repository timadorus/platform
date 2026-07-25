// Package events holds Object's domain events and their registry hookup only — no business
// logic (same shape as domain/universe/events; see that package's doc comment).
package events

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType is the stable string used to namespace this aggregate's events in the event
// store and its subject on the event bus.
const AggregateType = "object"

const (
	TypeObjectCreated  = "object.created.v1"
	TypeObjectRenamed  = "object.renamed.v1"
	TypeObjectArchived = "object.archived.v1"
)

// ObjectCreated carries UniverseID (the immutable parent reference) since that isn't
// derivable from the envelope (plan §4.5).
type ObjectCreated struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	UniverseID uuid.UUID `json:"universeId"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (ObjectCreated) EventType() string { return TypeObjectCreated }

type ObjectRenamed struct {
	Name       string    `json:"name"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (ObjectRenamed) EventType() string { return TypeObjectRenamed }

type ObjectArchived struct {
	OccurredAt time.Time `json:"occurredAt"`
}

func (ObjectArchived) EventType() string { return TypeObjectArchived }

// Register hooks every Object event into reg so infrastructure can deserialize persisted
// payloads back into these concrete types during replay.
func Register(reg *eventsourcing.Registry) {
	reg.Register(TypeObjectCreated, func() eventsourcing.Event { return &ObjectCreated{} })
	reg.Register(TypeObjectRenamed, func() eventsourcing.Event { return &ObjectRenamed{} })
	reg.Register(TypeObjectArchived, func() eventsourcing.Event { return &ObjectArchived{} })
}
