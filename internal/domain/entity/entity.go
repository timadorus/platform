// Package entity is a parented aggregate with no collection invariant of its own — just an
// immutable UniverseID reference plus Create/Rename/Archive. It's created either directly
// (POST /universes/{universeId}/entities) or implicitly alongside a Character (plan §4.4);
// either way this constructor is the same, the caller decides how it's persisted.
package entity

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/entity/events"
	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType re-exports events.AggregateType — see domain/universe.AggregateType for why.
const AggregateType = events.AggregateType

type Entity struct {
	eventsourcing.Base

	name       string
	universeID uuid.UUID
	archived   bool
}

func (e *Entity) Name() string          { return e.name }
func (e *Entity) UniverseID() uuid.UUID { return e.universeID }
func (e *Entity) IsArchived() bool      { return e.archived }

// New constructs and creates a new Entity under universeID. The caller (the application
// command service) is responsible for having already validated that universeID refers to an
// existing, non-archived Universe (plan §4.3) — this constructor only enforces the
// aggregate's own invariant (non-blank name).
func New(universeID uuid.UUID, name string) (*Entity, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	e := &Entity{}
	e.raise(&events.EntityCreated{ID: uuid.New(), Name: name, UniverseID: universeID, OccurredAt: time.Now().UTC()})
	return e, nil
}

func (e *Entity) Rename(name string) error {
	if e.archived {
		return ErrArchived
	}
	if name == "" {
		return ErrNameRequired
	}
	if name == e.name {
		return nil
	}
	e.raise(&events.EntityRenamed{Name: name, OccurredAt: time.Now().UTC()})
	return nil
}

// Archive is idempotent — see universe.Universe.Archive's doc comment for why.
func (e *Entity) Archive() error {
	if e.archived {
		return nil
	}
	e.raise(&events.EntityArchived{OccurredAt: time.Now().UTC()})
	return nil
}

func (e *Entity) Apply(event eventsourcing.Event) {
	switch ev := event.(type) {
	case *events.EntityCreated:
		e.SetID(ev.ID)
		e.name = ev.Name
		e.universeID = ev.UniverseID
	case *events.EntityRenamed:
		e.name = ev.Name
	case *events.EntityArchived:
		e.archived = true
	}
}

func (e *Entity) raise(event eventsourcing.Event) {
	e.Base.Raise(e, event)
}
