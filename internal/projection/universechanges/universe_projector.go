package universechanges

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/universe/events"
)

const UniverseProjectorName = "universe-changes-universe"

// UniverseProjector is the simplest of the five: a Universe event's own aggregate id already is
// the Universe id, so no resolution step is needed at all.
type UniverseProjector struct{}

func NewUniverseProjector() *UniverseProjector { return &UniverseProjector{} }

func (p *UniverseProjector) Name() string { return UniverseProjectorName }

func (p *UniverseProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *UniverseProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	return insertChange(ctx, tx, env.AggregateID, env)
}
