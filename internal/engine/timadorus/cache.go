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

// RulesetCache caches each Campaign's Ruleset name, keyed by campaign id. A Campaign's Ruleset
// reference is set once at creation and never changes (no such command exists on Campaign), so
// once resolved, a lookup never needs repeating. Shared by both CharacterProcessor and
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
