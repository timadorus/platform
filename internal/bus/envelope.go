package bus

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Envelope is the wire format published to NATS by the outbox relay (internal/outbox) and
// consumed by projections (internal/projection). It carries the aggregate identity fields
// that event payloads deliberately omit for their own aggregate (see plan §4.5's
// convention: an event never repeats its own aggregate's id/type/version, only ids
// pointing at a *different* aggregate) — the envelope is the only place a projector can
// learn "who" this event happened to.
type Envelope struct {
	GlobalSeq     int64           `json:"globalSeq"`
	AggregateID   uuid.UUID       `json:"aggregateId"`
	AggregateType string          `json:"aggregateType"`
	Version       int             `json:"version"`
	EventType     string          `json:"eventType"`
	Payload       json.RawMessage `json:"payload"`
	Metadata      json.RawMessage `json:"metadata"`
	// CreatedAt is the event's original creation time (events.created_at), carried through
	// so projections can measure their own lag (internal/observability.ProjectionLagSeconds)
	// without a round-trip back to the event store.
	CreatedAt time.Time `json:"createdAt"`
}

// CorrelationID best-effort extracts the correlation_id stamped into Metadata by
// internal/eventstore/postgres.Store.Append (see that package's eventMetadata type).
// Returns "" if Metadata is empty, malformed, or has no correlation_id — never an error,
// since this is only ever used for logging.
func (e Envelope) CorrelationID() string {
	if len(e.Metadata) == 0 {
		return ""
	}
	var m struct {
		CorrelationID string `json:"correlation_id"`
	}
	if err := json.Unmarshal(e.Metadata, &m); err != nil {
		return ""
	}
	return m.CorrelationID
}
