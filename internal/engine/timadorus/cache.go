// Package timadorus is timadorus-engine's Processor: an internal/projection.Projector that
// reacts to Character ActionRequested events, conditionally (based on the Character's
// Campaign's Ruleset name) appending a timestamp to the Character's info field. See
// docs/superpowers/specs/2026-08-23-character-action-timadorus-engine-design.md.
package timadorus

import (
	"sync"

	"github.com/google/uuid"
)

// rulesetCache caches each Campaign's Ruleset name, keyed by campaign id. A Campaign's Ruleset
// reference is set once at creation and never changes (no such command exists on Campaign), so
// once resolved, a lookup never needs repeating — even though Handle runs on every incoming
// Character event, not just the ones that end up matching. Guarded by a mutex for
// defensiveness only: Processor.Handle runs on a single goroutine today (one subject,
// SubscribersCount: 1 — internal/bus.NewSubscriber's doc comment), mirroring how
// projection.Router itself guards its own single-goroutine-today attempts map
// (internal/projection/router.go).
type rulesetCache struct {
	mu    sync.RWMutex
	names map[uuid.UUID]string
}

func newRulesetCache() *rulesetCache {
	return &rulesetCache{names: make(map[uuid.UUID]string)}
}

func (c *rulesetCache) get(campaignID uuid.UUID) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	name, ok := c.names[campaignID]
	return name, ok
}

// set is only ever called after a fully successful lookup (see Processor.rulesetName) — a
// not-found/error result is never cached, so a transient "campaign not projected yet" failure
// doesn't poison the cache; the next redelivery attempt just re-queries.
func (c *rulesetCache) set(campaignID uuid.UUID, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.names[campaignID] = name
}
