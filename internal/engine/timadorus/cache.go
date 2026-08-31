// Package timadorus is timadorus-engine's event processors: internal/projection.Projector
// implementations that react to Character ActionRequested and Campaign ConfigurationRequested
// events, conditionally (based on the relevant Campaign's Ruleset name) appending a timestamp
// to that aggregate's opaque string field. See
// docs/superpowers/specs/2026-08-23-character-action-timadorus-engine-design.md and
// docs/superpowers/specs/2026-08-23-campaign-configuration-timadorus-engine-design.md.
package timadorus

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// errPrefix prefixes every error this package's processors return, so a future rename can't
// silently leave a stale string behind in error messages.
const errPrefix = "timadorus-engine: "

// RulesetCache caches each Campaign's Ruleset name, keyed by campaign id, to avoid repeating
// the same joined lookup query on every event. The Ruleset id a Campaign references is set once
// at creation and never changes (no such command exists on Campaign) — but the cached value
// here is the Ruleset's name, not its id, and Ruleset.Rename does exist (exposed as PATCH
// /rulesets/{rulesetId}, projected into rulesets_read_model.name). This cache does not observe
// RulesetRenamed: once a campaign's ruleset name is cached, a later rename to or away from that
// name has no effect for that campaign until the process restarts. This is an accepted
// trade-off for now, not a correctness guarantee — a follow-up could add invalidation on
// RulesetRenamed if this ever matters in practice. Shared by both CharacterProcessor and
// CampaignProcessor (constructed once in cmd/timadorus-engine/main.go and injected into both) —
// a Character-triggered lookup and a Campaign-triggered lookup for the same campaign id resolve
// to the same cached entry. Guarded by a mutex for defensiveness only: each Processor's Handle
// runs on a single goroutine today (one subject per processor, SubscribersCount: 1 —
// internal/bus.NewSubscriber's doc comment), mirroring how projection.Router itself guards its
// own single-goroutine-today attempts map (internal/projection/router.go).
type RulesetCache struct {
	mu    sync.RWMutex
	names map[uuid.UUID]string
}

func NewRulesetCache() *RulesetCache {
	return &RulesetCache{names: make(map[uuid.UUID]string)}
}

func (c *RulesetCache) get(campaignID uuid.UUID) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	name, ok := c.names[campaignID]
	return name, ok
}

// set is only ever called by resolve after a fully successful lookup — a not-found/error result
// is never cached, so a transient "campaign not projected yet" failure doesn't poison the cache;
// the next redelivery attempt just re-queries.
func (c *RulesetCache) set(campaignID uuid.UUID, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.names[campaignID] = name
}

// resolve returns campaignID's Ruleset name, from cache if present, otherwise via the one
// joined query both processors need — called identically by CharacterProcessor (after an extra
// characters_read_model hop to find the campaign id) and CampaignProcessor (whose own
// env.AggregateID already is the campaign id).
func (c *RulesetCache) resolve(ctx context.Context, tx pgx.Tx, campaignID uuid.UUID) (string, error) {
	if name, ok := c.get(campaignID); ok {
		return name, nil
	}

	var name string
	err := tx.QueryRow(ctx,
		`SELECT r.name FROM campaigns_read_model c
		 JOIN rulesets_read_model r ON r.id = c.ruleset_id
		 WHERE c.id = $1`, campaignID,
	).Scan(&name)
	if err != nil {
		return "", fmt.Errorf(errPrefix+"look up ruleset for campaign %s: %w", campaignID, err)
	}

	c.set(campaignID, name)
	return name, nil
}

// resolveByRulesetID is resolve's sibling for callers that already know the Ruleset id directly
// (CampaignProcessor's CampaignCreated handling, whose triggering event carries RulesetID) rather
// than only the campaign id. Unlike resolve, this never joins through campaigns_read_model — that
// table's row for a freshly created Campaign is written by a different, independently-racing
// projector consuming the exact same CampaignCreated event this method is called for, with no
// ordering guarantee between the two. Querying rulesets_read_model directly by its own primary
// key sidesteps that race entirely: a Ruleset must already exist (and, in practice, has almost
// certainly been projected already — it was created in an earlier, already-completed request)
// before any Campaign can reference it. The resolved name is cached under campaignID via the same
// map resolve uses, so a call to resolve for the same campaign right after this one hits the
// cache instead of re-running the (racy) join at all.
func (c *RulesetCache) resolveByRulesetID(ctx context.Context, tx pgx.Tx, campaignID, rulesetID uuid.UUID) (string, error) {
	if name, ok := c.get(campaignID); ok {
		return name, nil
	}

	var name string
	err := tx.QueryRow(ctx,
		`SELECT name FROM rulesets_read_model WHERE id = $1`, rulesetID,
	).Scan(&name)
	if err != nil {
		return "", fmt.Errorf(errPrefix+"look up ruleset %s for campaign %s: %w", rulesetID, campaignID, err)
	}

	c.set(campaignID, name)
	return name, nil
}
