package universechanges

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign/events"
)

const CampaignProjectorName = "universe-changes-campaign"

type CampaignProjector struct{}

func NewCampaignProjector() *CampaignProjector { return &CampaignProjector{} }

func (p *CampaignProjector) Name() string { return CampaignProjectorName }

func (p *CampaignProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *CampaignProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := p.resolveUniverseID(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}

// resolveUniverseID reads UniverseID straight from CampaignCreated's own payload (free — every
// Campaign carries this immutably from creation). Every other Campaign event carries only its
// own aggregate id, so it's resolved via a plain read-model query — the same
// cross-projection-read pattern RulesetCache.resolve already established, including the same
// narrow "immediately after creation" race window: a zero-rows result here is a plain wrapped
// error, which the router's own Nack/retry path already handles exactly like every other
// cross-projection lookup in this codebase.
func (p *CampaignProjector) resolveUniverseID(ctx context.Context, tx pgx.Tx, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == events.TypeCampaignCreated {
		var e events.CampaignCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("universechanges: unmarshal %s: %w", env.EventType, err)
		}
		return e.UniverseID, nil
	}

	var universeID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT universe_id FROM campaigns_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("universechanges: resolve universe for campaign %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}
