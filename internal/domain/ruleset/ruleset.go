// Package ruleset is an independent, top-level aggregate with no parent (same shape as
// domain/user) representing a reusable game system that a Campaign references immutably at
// creation (plan §2). Unlike User/Entity/Object, it has two mutable fields beyond name —
// description and references — each replaced wholesale by its own command rather than
// incrementally, since neither has a minimum-count invariant to protect.
package ruleset

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/ruleset/events"
	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType re-exports events.AggregateType — see domain/universe.AggregateType for why.
const AggregateType = events.AggregateType

type Ruleset struct {
	eventsourcing.Base

	name        string
	description string
	references  []string
	archived    bool
}

func (r *Ruleset) Name() string         { return r.name }
func (r *Ruleset) Description() string  { return r.description }
func (r *Ruleset) References() []string { return r.references }
func (r *Ruleset) IsArchived() bool     { return r.archived }

func New(name, description string, references []string) (*Ruleset, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	r := &Ruleset{}
	r.raise(&events.RulesetCreated{
		ID:          uuid.New(),
		Name:        name,
		Description: description,
		References:  references,
		OccurredAt:  time.Now().UTC(),
	})
	return r, nil
}

func (r *Ruleset) Rename(name string) error {
	if r.archived {
		return ErrArchived
	}
	if name == "" {
		return ErrNameRequired
	}
	if name == r.name {
		return nil
	}
	r.raise(&events.RulesetRenamed{Name: name, OccurredAt: time.Now().UTC()})
	return nil
}

func (r *Ruleset) SetDescription(description string) error {
	if r.archived {
		return ErrArchived
	}
	r.raise(&events.RulesetDescriptionChanged{Description: description, OccurredAt: time.Now().UTC()})
	return nil
}

func (r *Ruleset) SetReferences(references []string) error {
	if r.archived {
		return ErrArchived
	}
	r.raise(&events.RulesetReferencesChanged{References: references, OccurredAt: time.Now().UTC()})
	return nil
}

// Archive is idempotent — see universe.Universe.Archive's doc comment for why.
func (r *Ruleset) Archive() error {
	if r.archived {
		return nil
	}
	r.raise(&events.RulesetArchived{OccurredAt: time.Now().UTC()})
	return nil
}

func (r *Ruleset) Apply(event eventsourcing.Event) {
	switch e := event.(type) {
	case *events.RulesetCreated:
		r.SetID(e.ID)
		r.name = e.Name
		r.description = e.Description
		r.references = e.References
	case *events.RulesetRenamed:
		r.name = e.Name
	case *events.RulesetDescriptionChanged:
		r.description = e.Description
	case *events.RulesetReferencesChanged:
		r.references = e.References
	case *events.RulesetArchived:
		r.archived = true
	}
}

func (r *Ruleset) raise(event eventsourcing.Event) {
	r.Base.Raise(r, event)
}
