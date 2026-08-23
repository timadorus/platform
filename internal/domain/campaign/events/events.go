// Package events holds Campaign's domain events and their registry hookup only — no
// business logic (same shape as domain/universe/events; see that package's doc comment).
package events

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType is the stable string used to namespace this aggregate's events in the event
// store and its subject on the event bus.
const AggregateType = "campaign"

const (
	TypeCampaignCreated        = "campaign.created.v1"
	TypeCampaignRenamed        = "campaign.renamed.v1"
	TypeGamemasterAdded        = "campaign.gamemaster_added.v1"
	TypeGamemasterRemove       = "campaign.gamemaster_removed.v1"
	TypeConfigurationChanged   = "campaign.configuration_changed.v1"
	TypeConfigurationRequested = "campaign.configuration_requested.v1"
	TypeCampaignArchived       = "campaign.archived.v1"
)

// CampaignCreated carries UniverseID and RulesetID (both immutable parent-like references)
// since neither is derivable from the envelope (plan §4.5).
type CampaignCreated struct {
	ID                uuid.UUID   `json:"id"`
	Name              string      `json:"name"`
	UniverseID        uuid.UUID   `json:"universeId"`
	RulesetID         uuid.UUID   `json:"rulesetId"`
	GamemasterUserIDs []uuid.UUID `json:"gamemasterUserIds"`
	OccurredAt        time.Time   `json:"occurredAt"`
}

func (CampaignCreated) EventType() string { return TypeCampaignCreated }

type CampaignRenamed struct {
	Name       string    `json:"name"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (CampaignRenamed) EventType() string { return TypeCampaignRenamed }

type GamemasterAdded struct {
	UserID     uuid.UUID `json:"userId"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (GamemasterAdded) EventType() string { return TypeGamemasterAdded }

type GamemasterRemoved struct {
	UserID     uuid.UUID `json:"userId"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (GamemasterRemoved) EventType() string { return TypeGamemasterRemove }

// ConfigurationChanged carries the Campaign's new configuration wholesale (replace, not merge
// — same shape as domain/character's InfoChanged).
type ConfigurationChanged struct {
	Configuration string    `json:"configuration"`
	OccurredAt    time.Time `json:"occurredAt"`
}

func (ConfigurationChanged) EventType() string { return TypeConfigurationChanged }

// ConfigurationRequested is a pure trigger: raised by Campaign.RequestConfiguration, applied as
// a no-op (see Campaign.Apply) — the actual effect, if any, is decided later and asynchronously
// by timadorus-engine's CampaignProcessor, conditionally on this Campaign's own Ruleset name,
// never by the Campaign aggregate itself. Payload is the PUT /campaigns/{id}/configure request
// body, re-marshaled to a canonical JSON string (same representation Configuration itself uses).
type ConfigurationRequested struct {
	Payload    string    `json:"payload"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (ConfigurationRequested) EventType() string { return TypeConfigurationRequested }

type CampaignArchived struct {
	OccurredAt time.Time `json:"occurredAt"`
}

func (CampaignArchived) EventType() string { return TypeCampaignArchived }

// Register hooks every Campaign event into reg so infrastructure can deserialize persisted
// payloads back into these concrete types during replay.
func Register(reg *eventsourcing.Registry) {
	reg.Register(TypeCampaignCreated, func() eventsourcing.Event { return &CampaignCreated{} })
	reg.Register(TypeCampaignRenamed, func() eventsourcing.Event { return &CampaignRenamed{} })
	reg.Register(TypeGamemasterAdded, func() eventsourcing.Event { return &GamemasterAdded{} })
	reg.Register(TypeGamemasterRemove, func() eventsourcing.Event { return &GamemasterRemoved{} })
	reg.Register(TypeConfigurationChanged, func() eventsourcing.Event { return &ConfigurationChanged{} })
	reg.Register(TypeConfigurationRequested, func() eventsourcing.Event { return &ConfigurationRequested{} })
	reg.Register(TypeCampaignArchived, func() eventsourcing.Event { return &CampaignArchived{} })
}
