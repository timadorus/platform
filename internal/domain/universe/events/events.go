// Package events holds Universe's domain events and their registry hookup only — no
// business logic. This is what lets the projector and query-api import Universe's events
// (to replay them into read models) without ever importing the invariant-bearing
// internal/domain/universe package itself.
package events

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType is the stable string used to namespace this aggregate's events in the event
// store and its subject on the event bus. Lives here (not in the domain package) so that
// infrastructure wiring on the read side (projector, query-api) can reference it without
// importing the invariant-bearing domain package.
const AggregateType = "universe"

const (
	TypeUniverseCreated  = "universe.created.v1"
	TypeUniverseRenamed  = "universe.renamed.v1"
	TypeCreatorAdded     = "universe.creator_added.v1"
	TypeCreatorRemoved   = "universe.creator_removed.v1"
	TypeUniverseArchived = "universe.archived.v1"
)

type UniverseCreated struct {
	ID             uuid.UUID   `json:"id"`
	Name           string      `json:"name"`
	CreatorUserIDs []uuid.UUID `json:"creatorUserIds"`
	OccurredAt     time.Time   `json:"occurredAt"`
}

func (UniverseCreated) EventType() string { return TypeUniverseCreated }

type UniverseRenamed struct {
	Name       string    `json:"name"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (UniverseRenamed) EventType() string { return TypeUniverseRenamed }

type CreatorAdded struct {
	UserID     uuid.UUID `json:"userId"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (CreatorAdded) EventType() string { return TypeCreatorAdded }

type CreatorRemoved struct {
	UserID     uuid.UUID `json:"userId"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (CreatorRemoved) EventType() string { return TypeCreatorRemoved }

type UniverseArchived struct {
	OccurredAt time.Time `json:"occurredAt"`
}

func (UniverseArchived) EventType() string { return TypeUniverseArchived }

// Register hooks every Universe event into reg so infrastructure can deserialize persisted
// payloads back into these concrete types during replay.
func Register(reg *eventsourcing.Registry) {
	reg.Register(TypeUniverseCreated, func() eventsourcing.Event { return &UniverseCreated{} })
	reg.Register(TypeUniverseRenamed, func() eventsourcing.Event { return &UniverseRenamed{} })
	reg.Register(TypeCreatorAdded, func() eventsourcing.Event { return &CreatorAdded{} })
	reg.Register(TypeCreatorRemoved, func() eventsourcing.Event { return &CreatorRemoved{} })
	reg.Register(TypeUniverseArchived, func() eventsourcing.Event { return &UniverseArchived{} })
}
