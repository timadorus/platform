// Package object is structurally identical to domain/entity — a parented aggregate with no
// collection invariant, just an immutable UniverseID reference plus Create/Rename/Archive.
// This is the "mechanical replication" phase's proof that the framework generalizes with
// zero new framework code (plan §12, Phase 4): copy-adapt from Entity, nothing else changes.
package object

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/object/events"
	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType re-exports events.AggregateType — see domain/universe.AggregateType for why.
const AggregateType = events.AggregateType

type Object struct {
	eventsourcing.Base

	name       string
	universeID uuid.UUID
	archived   bool
}

func (o *Object) Name() string          { return o.name }
func (o *Object) UniverseID() uuid.UUID { return o.universeID }
func (o *Object) IsArchived() bool      { return o.archived }

// New constructs and creates a new Object under universeID. The caller (the application
// command service) is responsible for having already validated that universeID refers to an
// existing, non-archived Universe (plan §4.3) — this constructor only enforces the
// aggregate's own invariant (non-blank name).
func New(universeID uuid.UUID, name string) (*Object, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	o := &Object{}
	o.raise(&events.ObjectCreated{ID: uuid.New(), Name: name, UniverseID: universeID, OccurredAt: time.Now().UTC()})
	return o, nil
}

func (o *Object) Rename(name string) error {
	if o.archived {
		return ErrArchived
	}
	if name == "" {
		return ErrNameRequired
	}
	if name == o.name {
		return nil
	}
	o.raise(&events.ObjectRenamed{Name: name, OccurredAt: time.Now().UTC()})
	return nil
}

// Archive is idempotent — see universe.Universe.Archive's doc comment for why.
func (o *Object) Archive() error {
	if o.archived {
		return nil
	}
	o.raise(&events.ObjectArchived{OccurredAt: time.Now().UTC()})
	return nil
}

func (o *Object) Apply(event eventsourcing.Event) {
	switch ev := event.(type) {
	case *events.ObjectCreated:
		o.SetID(ev.ID)
		o.name = ev.Name
		o.universeID = ev.UniverseID
	case *events.ObjectRenamed:
		o.name = ev.Name
	case *events.ObjectArchived:
		o.archived = true
	}
}

func (o *Object) raise(event eventsourcing.Event) {
	o.Base.Raise(o, event)
}
