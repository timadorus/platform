// Package realtimehub is cmd/realtime's in-memory connection registry and fan-out point — the
// one thing that turns "an event arrived on NATS, already resolved to its owning Universe/
// Campaign" into "deliver it to every currently-matching SSE connection." See
// docs/superpowers/specs/2026-09-12-realtime-aggregate-updates-design.md Decisions 3 and 5.
package realtimehub

import "sync"

// Change is what a matched event becomes for delivery to a browser — the same shape
// web/src/composables/useChangeFeed.ts's AggregateChange already expects, so the SSE payload
// needs no frontend-side translation.
type Change struct {
	GlobalSeq     int64  `json:"globalSeq"`
	AggregateType string `json:"aggregateType"`
	AggregateID   string `json:"aggregateId"`
	EventType     string `json:"eventType"`
	OccurredAt    string `json:"occurredAt"`
}

// ResolvedEvent is one NATS event after cmd/realtime has resolved its UniverseID (and, for
// Character events, CampaignID) — what Broadcast matches WatchClauses against, and, on a match,
// delivers as a Change.
type ResolvedEvent struct {
	Change
	UniverseID string
	CampaignID string // only ever set when AggregateType == "character"
}

// WatchClause is one filter a connected client asks to be notified about — see the design spec's
// Decision 3 for the full ten-entry vocabulary. Exactly one of AggregateID/UniverseID/CampaignID
// is set, except a bare {Type: "universe"} clause (all three empty), which matches every Universe
// event unconditionally (the "watch every Universe" picker-list case — the only type with no
// meaningful scope to narrow by).
type WatchClause struct {
	Type        string `json:"type"`
	AggregateID string `json:"aggregateId,omitempty"`
	UniverseID  string `json:"universeId,omitempty"`
	CampaignID  string `json:"campaignId,omitempty"`
}

// Matches reports whether event satisfies clause.
func (clause WatchClause) Matches(event ResolvedEvent) bool {
	if clause.Type != event.AggregateType {
		return false
	}
	switch {
	case clause.AggregateID != "":
		return clause.AggregateID == event.AggregateID
	case clause.UniverseID != "":
		return clause.UniverseID == event.UniverseID
	case clause.CampaignID != "":
		return clause.CampaignID == event.CampaignID
	default:
		return clause.Type == "universe"
	}
}

// clientBufferSize bounds how many unconsumed Changes a slow client can accumulate before Hub
// disconnects it (see Broadcast) rather than blocking delivery to every other client — a
// disconnected client simply reconnects and catches up via the existing polling endpoint (design
// spec Decision 6), so dropping it is safe, not lossy in any way that matters.
const clientBufferSize = 32

type client struct {
	clauses   []WatchClause
	out       chan Change
	closeOnce sync.Once
}

// disconnect removes c from the registry (if the caller hasn't already) and closes its channel
// — draining any already-buffered-but-unread values first, so a client being disconnected for
// falling behind observes a clean, immediately-closed channel rather than dribbling out a stale
// backlog before it can tell it's gone. Safe to call more than once (e.g. once from Broadcast's
// own overflow path, once from the SSE handler's deferred unregister call) — closeOnce guards
// the actual close.
func (c *client) disconnect() {
	c.closeOnce.Do(func() {
		for {
			select {
			case <-c.out:
			default:
				close(c.out)
				return
			}
		}
	})
}

// Hub is safe for concurrent use — Register/Broadcast (and the unregister function Register
// returns) are called from different goroutines (one per SSE connection, one per NATS subject).
type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
}

func NewHub() *Hub {
	return &Hub{clients: make(map[*client]struct{})}
}

// Register adds a new client with clauses, returning its outbound channel and an unregister
// function the caller must invoke exactly once (typically via defer) when the connection ends.
// unregister is safe to call even if Broadcast has already disconnected this client itself (see
// Broadcast's own doc comment) — closeOnce (inside disconnect) makes double-disconnecting harmless.
func (h *Hub) Register(clauses []WatchClause) (out <-chan Change, unregister func()) {
	c := &client{clauses: clauses, out: make(chan Change, clientBufferSize)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c.out, func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
		c.disconnect()
	}
}

// Broadcast delivers event to every currently-registered client whose filter matches. A client
// whose buffer is already full is disconnected (removed from the registry, its channel drained
// and closed) rather than allowed to block delivery to every other client — deleting from
// h.clients while ranging over it is safe (Go guarantees this), and holding the single mutex for
// the whole call (rather than an RLock that would race a concurrent delete) is what keeps this
// correct without a separate remove path that would need to re-acquire the same lock reentrantly.
func (h *Hub) Broadcast(event ResolvedEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		matched := false
		for _, clause := range c.clauses {
			if clause.Matches(event) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		select {
		case c.out <- event.Change:
		default:
			delete(h.clients, c)
			c.disconnect()
		}
	}
}
