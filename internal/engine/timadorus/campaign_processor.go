package timadorus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign"
	"github.com/timadorus/platform/internal/domain/campaign/events"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
	"github.com/timadorus/platform/internal/observability"
)

// CampaignProcessorName is both the durable JetStream consumer name and the checkpoint table
// key — distinct from CharacterProcessorName so both processors get their own independent
// checkpoint row in the shared projection_checkpoints table, even though they share a binary
// and a RulesetCache.
const CampaignProcessorName = "timadorus-engine-campaign"

// CampaignProcessor is CharacterProcessor's sibling: same shape, same rationale for living in
// this binary, reacting to Campaign's own ConfigurationRequested instead of Character's
// ActionRequested. Its ruleset lookup is simpler than CharacterProcessor's: a Campaign event's
// own env.AggregateID already is the campaign id, so no characters_read_model hop is needed.
// Not naturally idempotent on replay, for the same reason CharacterProcessor isn't — see that
// type's doc comment.
type CampaignProcessor struct {
	campaigns *eventsourcing.Repository[*campaign.Campaign]
	cache     *RulesetCache
}

// NewCampaignProcessor mirrors NewCharacterProcessor's construction, scoped to Campaign. cache
// is shared with CharacterProcessor — see RulesetCache's doc comment.
func NewCampaignProcessor(pool *pgxpool.Pool, cache *RulesetCache) *CampaignProcessor {
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	return &CampaignProcessor{
		campaigns: eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
			return &campaign.Campaign{}
		}),
		cache: cache,
	}
}

func (p *CampaignProcessor) Name() string { return CampaignProcessorName }

func (p *CampaignProcessor) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *CampaignProcessor) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	if env.EventType != events.TypeConfigurationRequested {
		return nil
	}
	var e events.ConfigurationRequested
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf(errPrefix+"unmarshal %s: %w", env.EventType, err)
	}

	// A Campaign event's own aggregate id already is the campaign id — no extra hop needed
	// (contrast CharacterProcessor.Handle, which first has to look up campaign_id from
	// characters_read_model).
	campaignID := env.AggregateID

	rulesetName, err := p.cache.resolve(ctx, tx, campaignID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(rulesetName, targetRulesetName) {
		return nil // not our ruleset — no-op, still checkpointed as handled
	}

	return p.appendConfigurationTimestamp(ctx, tx, env, e.OccurredAt)
}

// appendConfigurationTimestamp mirrors CharacterProcessor.appendActionTimestamp exactly, with
// two differences: it operates on Campaign/Configuration/SetConfiguration instead of
// Character/Info/SetInfo, and the JSON list key is "configs", not "actions" (deliberately
// different from Character's shape).
func (p *CampaignProcessor) appendConfigurationTimestamp(ctx context.Context, tx pgx.Tx, env bus.Envelope, occurredAt time.Time) error {
	campaignID := env.AggregateID
	txCtx := postgres.WithTx(observability.WithCorrelationID(ctx, env.CorrelationID()), tx)

	c, err := p.campaigns.Load(txCtx, campaignID)
	if err != nil {
		return fmt.Errorf(errPrefix+"load campaign %s: %w", campaignID, err)
	}

	// Parse into a generic map, touching only "configs" — never discard other top-level keys a
	// human might have set via the already-existing PUT .../configuration endpoint. If the
	// existing configuration isn't valid JSON, or isn't a JSON object at all (allowed today:
	// SetConfiguration accepts any string), start fresh rather than erroring — retrying won't
	// fix malformed content, so erroring here would get this Campaign permanently stuck
	// instead of self-healing.
	config := map[string]any{}
	if raw := c.Configuration(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &config) // best-effort; config stays {} on failure
	}
	configs, _ := config["configs"].([]any)
	config["configs"] = append(configs, occurredAt.UTC().Format(time.RFC3339Nano))

	newConfig, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf(errPrefix+"marshal updated configuration for campaign %s: %w", campaignID, err)
	}

	if err := c.SetConfiguration(string(newConfig)); err != nil {
		// A Campaign archived between the PUT .../configure request and this engine processing
		// the resulting ConfigurationRequested is a legitimate, expected race — same rationale
		// as CharacterProcessor.appendActionTimestamp's identical guard.
		if errors.Is(err, campaign.ErrArchived) {
			return nil
		}
		return fmt.Errorf(errPrefix+"set configuration for campaign %s: %w", campaignID, err)
	}
	if err := p.campaigns.Save(txCtx, c); err != nil {
		return fmt.Errorf(errPrefix+"save campaign %s: %w", campaignID, err)
	}
	return nil
}
