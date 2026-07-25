// Package events holds User's domain events and their registry hookup only — no business
// logic (same shape as domain/universe/events; see that package's doc comment for why).
package events

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType is the stable string used to namespace this aggregate's events in the event
// store and its subject on the event bus.
const AggregateType = "user"

const (
	TypeUserCreated  = "user.created.v1"
	TypeUserRenamed  = "user.renamed.v1"
	TypeUserArchived = "user.archived.v1"
)

type UserCreated struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (UserCreated) EventType() string { return TypeUserCreated }

type UserRenamed struct {
	Name       string    `json:"name"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (UserRenamed) EventType() string { return TypeUserRenamed }

type UserArchived struct {
	OccurredAt time.Time `json:"occurredAt"`
}

func (UserArchived) EventType() string { return TypeUserArchived }

// Register hooks every User event into reg so infrastructure can deserialize persisted
// payloads back into these concrete types during replay.
func Register(reg *eventsourcing.Registry) {
	reg.Register(TypeUserCreated, func() eventsourcing.Event { return &UserCreated{} })
	reg.Register(TypeUserRenamed, func() eventsourcing.Event { return &UserRenamed{} })
	reg.Register(TypeUserArchived, func() eventsourcing.Event { return &UserArchived{} })
}
