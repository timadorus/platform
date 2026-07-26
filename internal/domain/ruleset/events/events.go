// Package events holds Ruleset's domain events and their registry hookup only — no business
// logic (same shape as domain/universe/events; see that package's doc comment).
package events

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType is the stable string used to namespace this aggregate's events in the event
// store and its subject on the event bus.
const AggregateType = "ruleset"

const (
	TypeRulesetCreated            = "ruleset.created.v1"
	TypeRulesetRenamed            = "ruleset.renamed.v1"
	TypeRulesetDescriptionChanged = "ruleset.description_changed.v1"
	TypeRulesetReferencesChanged  = "ruleset.references_changed.v1"
	TypeRulesetArchived           = "ruleset.archived.v1"
)

type RulesetCreated struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	References  []string  `json:"references"`
	OccurredAt  time.Time `json:"occurredAt"`
}

func (RulesetCreated) EventType() string { return TypeRulesetCreated }

type RulesetRenamed struct {
	Name       string    `json:"name"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (RulesetRenamed) EventType() string { return TypeRulesetRenamed }

type RulesetDescriptionChanged struct {
	Description string    `json:"description"`
	OccurredAt  time.Time `json:"occurredAt"`
}

func (RulesetDescriptionChanged) EventType() string { return TypeRulesetDescriptionChanged }

type RulesetReferencesChanged struct {
	References []string  `json:"references"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (RulesetReferencesChanged) EventType() string { return TypeRulesetReferencesChanged }

type RulesetArchived struct {
	OccurredAt time.Time `json:"occurredAt"`
}

func (RulesetArchived) EventType() string { return TypeRulesetArchived }

// Register hooks every Ruleset event into reg so infrastructure can deserialize persisted
// payloads back into these concrete types during replay.
func Register(reg *eventsourcing.Registry) {
	reg.Register(TypeRulesetCreated, func() eventsourcing.Event { return &RulesetCreated{} })
	reg.Register(TypeRulesetRenamed, func() eventsourcing.Event { return &RulesetRenamed{} })
	reg.Register(TypeRulesetDescriptionChanged, func() eventsourcing.Event { return &RulesetDescriptionChanged{} })
	reg.Register(TypeRulesetReferencesChanged, func() eventsourcing.Event { return &RulesetReferencesChanged{} })
	reg.Register(TypeRulesetArchived, func() eventsourcing.Event { return &RulesetArchived{} })
}
