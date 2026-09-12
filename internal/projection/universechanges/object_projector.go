package universechanges

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/aggregateresolve"
	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/object/events"
)

const objectProjectorName = "universe-changes-object"

type ObjectProjector struct{}

func NewObjectProjector() *ObjectProjector { return &ObjectProjector{} }

func (p *ObjectProjector) Name() string { return objectProjectorName }

func (p *ObjectProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

// Handle resolves this Object event's own UniverseID via the shared internal/aggregateresolve
// package — see CampaignProjector.Handle's doc comment for the full reasoning, which applies
// identically here.
func (p *ObjectProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := aggregateresolve.Object(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}
