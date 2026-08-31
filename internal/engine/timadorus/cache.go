// Package timadorus is timadorus-engine's event processors: internal/projection.Projector
// implementations that react to Character ActionRequested, Campaign ConfigurationRequested, and
// Campaign CampaignCreated events, conditionally (based on the relevant Campaign's Ruleset name)
// either appending a timestamp to that aggregate's opaque string field or merging in the target
// Ruleset's default traits. See
// docs/superpowers/specs/2026-08-23-character-action-timadorus-engine-design.md,
// docs/superpowers/specs/2026-08-23-campaign-configuration-timadorus-engine-design.md, and
// docs/superpowers/specs/2026-08-31-campaign-creation-default-traits-design.md.
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
// own single-goroutine-today attempts map (internal/projection/router.go). The cache has no
// eviction or TTL: it grows by one entry per Campaign ever created (CampaignProcessor's
// handleCampaignCreated populates it for every CampaignCreated, matching ruleset or not) and
// entries live for the process lifetime. Not a practical problem at this platform's scale, but
// worth naming — an unbounded map keyed by an ever-growing id space — in the same spirit as the
// RulesetRenamed staleness trade-off documented above.
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

// set is only ever called after a fully successful lookup — by resolve's own read-model join, or
// by CampaignProcessor.handleCampaignCreated's event-store load — a not-found/error result is
// never cached, so a transient "campaign not projected yet" (or "ruleset not found") failure
// doesn't poison the cache; the next redelivery attempt just re-queries.
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
