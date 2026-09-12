package universechanges

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/aggregateresolve"
	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/entity/events"
)

const entityProjectorName = "universe-changes-entity"

type EntityProjector struct{}

func NewEntityProjector() *EntityProjector { return &EntityProjector{} }

func (p *EntityProjector) Name() string { return entityProjectorName }

func (p *EntityProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

// Handle resolves this Entity event's own UniverseID via the shared internal/aggregateresolve
// package — see CampaignProjector.Handle's doc comment for the full reasoning, which applies
// identically here.
func (p *EntityProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := aggregateresolve.Entity(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}
