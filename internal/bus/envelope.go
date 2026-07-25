package bus

import (
	"encoding/json"

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
}
