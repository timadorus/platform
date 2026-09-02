package timadorus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign"
	"github.com/timadorus/platform/internal/domain/campaign/events"
	"github.com/timadorus/platform/internal/domain/ruleset"
	rulesetevents "github.com/timadorus/platform/internal/domain/ruleset/events"
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
// this binary, reacting to two of Campaign's own events — CampaignCreated (merging default
// traits) and ConfigurationRequested (appending a timestamp) — instead of Character's single
// ActionRequested. Its ruleset lookup for ConfigurationRequested is simpler than
// CharacterProcessor's: a Campaign event's own env.AggregateID already is the campaign id, so no
// characters_read_model hop is needed; that lookup goes through RulesetCache.resolve's
// campaigns_read_model join. CampaignCreated resolves its Ruleset name a different way — loading
// the Ruleset aggregate directly from the event store via p.rulesets, keyed by the event's own
// RulesetID — since neither campaigns_read_model nor rulesets_read_model can be relied on to
// have a row for a brand-new Campaign or its Ruleset yet (see handleCampaignCreated's doc
// comment for why the event store is race-free here and a read model isn't). Not naturally
// idempotent on replay for the ConfigurationRequested path (the "configs" append), for the same
// reason CharacterProcessor isn't — see that type's doc comment. The CampaignCreated path (a
// plain "traits" overwrite) is naturally idempotent on replay by contrast.
type CampaignProcessor struct {
	campaigns *eventsourcing.Repository[*campaign.Campaign]
	rulesets  *eventsourcing.Repository[*ruleset.Ruleset]
	cache     *RulesetCache
}

// NewCampaignProcessor mirrors NewCharacterProcessor's construction, scoped to Campaign. cache
// is shared with CharacterProcessor — see RulesetCache's doc comment. Also builds its own
// Ruleset repository, scoped to just reading Ruleset aggregates by id (never mutates one) —
// mirrors register.go's own scoped ruleset registry/store/repo construction. Used by
// handleCampaignCreated to resolve a Ruleset's name directly from the event store rather than
// rulesets_read_model — see that method's doc comment for why.
func NewCampaignProcessor(pool *pgxpool.Pool, cache *RulesetCache) *CampaignProcessor {
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	rulesetRegistry := eventsourcing.NewRegistry()
	rulesetevents.Register(rulesetRegistry)
	rulesetStore := postgres.NewStore(pool, rulesetRegistry)

	return &CampaignProcessor{
		campaigns: eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
			return &campaign.Campaign{}
		}),
		rulesets: eventsourcing.NewRepository(rulesetStore, ruleset.AggregateType, func() *ruleset.Ruleset {
			return &ruleset.Ruleset{}
		}),
		cache: cache,
	}
}

func (p *CampaignProcessor) Name() string { return CampaignProcessorName }

func (p *CampaignProcessor) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

// targetRulesetName's sibling: the "timadorus" Ruleset's default set of Traits, merged into
// every newly created Campaign that uses that Ruleset (see handleCampaignCreated). Not a `const`
// — Go has no slice constants — but never mutated after initialization; edit this list in place
// to change what new "timadorus" Campaigns start with.
var defaultTraits = []string{"strong", "agile", "quick"}

func (p *CampaignProcessor) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	switch env.EventType {
	case events.TypeCampaignCreated:
		return p.handleCampaignCreated(ctx, tx, env)
	case events.TypeConfigurationRequested:
		return p.handleConfigurationRequested(ctx, tx, env)
	default:
		return nil
	}
}

// handleCampaignCreated merges defaultTraits into a newly created Campaign's configuration, but
// only if that Campaign uses the "timadorus" Ruleset. Resolves the Ruleset name directly from the
// event store (via p.rulesets, keyed by the event's own RulesetID) rather than
// rulesets_read_model: that table is written by a different, independently-racing projector
// (internal/projection/ruleset, in a separate cmd/projector Deployment) consuming the same
// RulesetCreated event, with no ordering guarantee relative to this handler's own processing of
// CampaignCreated. Loading from the event store is race-free by construction instead:
// command/campaign.Service.Create already validates the referenced Ruleset against the event
// store (not the read model), so RulesetCreated is always durably committed before any Campaign
// can reference that id — there is no window where this Load can fail for a legitimately
// referenced Ruleset. The resolved name is cached under campaignID via the shared RulesetCache,
// so a ConfigurationRequested arriving for the same campaign shortly after creation gets a fast
// cache hit instead of its own campaigns_read_model join (see RulesetCache's doc comment).
func (p *CampaignProcessor) handleCampaignCreated(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.CampaignCreated
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf(errPrefix+"unmarshal %s: %w", env.EventType, err)
	}
	campaignID := env.AggregateID

	rulesetName, ok := p.cache.get(campaignID)
	if !ok {
		rulesetTxCtx := postgres.WithTx(ctx, tx)
		rs, err := p.rulesets.Load(rulesetTxCtx, e.RulesetID)
		if err != nil {
			return fmt.Errorf(errPrefix+"load ruleset %s for campaign %s: %w", e.RulesetID, campaignID, err)
		}
		rulesetName = rs.Name()
		p.cache.set(campaignID, rulesetName)
	}

	if !strings.EqualFold(rulesetName, targetRulesetName) {
		return nil // not our ruleset — no-op, still checkpointed as handled
	}

	return p.mutateConfiguration(ctx, tx, env, campaignID, func(config map[string]any) {
		config["traits"] = defaultTraits
	})
}

// handleConfigurationRequested mirrors handleCampaignCreated's shape, appending occurredAt to
// the "configs" array instead of overwriting "traits" — the one difference being this mutation
// is not idempotent under event replay (see mutateConfiguration's own doc comment).
func (p *CampaignProcessor) handleConfigurationRequested(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.ConfigurationRequested
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf(errPrefix+"unmarshal %s: %w", env.EventType, err)
	}

	campaignID := env.AggregateID
	rulesetName, err := p.cache.resolve(ctx, tx, campaignID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(rulesetName, targetRulesetName) {
		return nil // not our ruleset — no-op, still checkpointed as handled
	}

	occurredAt := e.OccurredAt
	return p.mutateConfiguration(ctx, tx, env, campaignID, func(config map[string]any) {
		configs, _ := config["configs"].([]any)
		config["configs"] = append(configs, occurredAt.UTC().Format(time.RFC3339Nano))
	})
}

// mutateConfiguration loads campaignID, parses its current Configuration into a generic map
// (starting fresh on any parse failure — the existing configuration isn't necessarily valid JSON
// or even a JSON object at all, since SetConfiguration accepts any string; erroring here would
// get a Campaign permanently stuck instead of self-healing on retry), applies mutate to update
// exactly the key(s) it owns, re-marshals, and saves via SetConfiguration. Shared by
// handleCampaignCreated ("traits", a plain idempotent overwrite) and
// handleConfigurationRequested ("configs", an append that is NOT idempotent under event replay —
// a checkpoint reset would re-append every historical timestamp rather than converge) so each
// only ever touches its own top-level key and never clobbers the other's.
func (p *CampaignProcessor) mutateConfiguration(ctx context.Context, tx pgx.Tx, env bus.Envelope, campaignID uuid.UUID, mutate func(map[string]any)) error {
	txCtx := postgres.WithTx(observability.WithCorrelationID(ctx, env.CorrelationID()), tx)

	c, err := p.campaigns.Load(txCtx, campaignID)
	if err != nil {
		return fmt.Errorf(errPrefix+"load campaign %s: %w", campaignID, err)
	}

	config := map[string]any{}
	if raw := c.Configuration(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &config) // best-effort; config stays {} on failure
	}
	mutate(config)

	newConfig, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf(errPrefix+"marshal updated configuration for campaign %s: %w", campaignID, err)
	}

	if err := c.SetConfiguration(string(newConfig)); err != nil {
		// A Campaign archived between the triggering request and this engine processing the
		// resulting event is a legitimate, expected race.
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
