package universechanges

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/aggregateresolve"
	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/character/events"
)

const characterProjectorName = "universe-changes-character"

type CharacterProjector struct{}

func NewCharacterProjector() *CharacterProjector { return &CharacterProjector{} }

func (p *CharacterProjector) Name() string { return characterProjectorName }

func (p *CharacterProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

// Handle resolves this Character event's own UniverseID via the shared internal/aggregateresolve
// package and records the change. aggregateresolve.Character also returns the Character's
// CampaignID — cmd/realtime needs it for campaign-scoped filtering, but this write side has no
// use for it, so it's discarded here.
func (p *CharacterProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, _, err := aggregateresolve.Character(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}
