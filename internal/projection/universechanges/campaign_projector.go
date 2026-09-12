package universechanges

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/aggregateresolve"
	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign/events"
)

const campaignProjectorName = "universe-changes-campaign"

type CampaignProjector struct{}

func NewCampaignProjector() *CampaignProjector { return &CampaignProjector{} }

func (p *CampaignProjector) Name() string { return campaignProjectorName }

func (p *CampaignProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

// Handle resolves this Campaign event's own UniverseID via the shared internal/aggregateresolve
// package (see that package's own doc comment for why it's shared with cmd/realtime) and records
// the change.
func (p *CampaignProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := aggregateresolve.Campaign(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}
