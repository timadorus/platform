package eventsourcing

import "github.com/google/uuid"

// Aggregate is the contract every domain aggregate type must satisfy. Aggregate authors
// only ever implement Apply themselves; every other method is provided for free by
// embedding Base.
type Aggregate interface {
	AggregateID() uuid.UUID
	Version() int
	Pending() []Event
	ClearPending()

	// SetID and SetVersion are used by Repository during replay/hydration. They are not
	// meant to be called from domain command methods.
	SetID(id uuid.UUID)
	SetVersion(v int)

	// Apply mutates in-memory state for a single event. It is called both for events
	// replayed from the store and for newly raised events, so it must be the single place
	// state (and therefore invariants derived from that state) is kept correct.
	Apply(event Event)
}

// Base is embedded by every concrete aggregate to provide identity, version tracking, and
// the raise-and-queue mechanism. Go has no "self type" for embedded structs, so Raise takes
// the owning aggregate explicitly.
type Base struct {
	id      uuid.UUID
	version int
	pending []Event
}

func (b *Base) AggregateID() uuid.UUID { return b.id }
func (b *Base) Version() int           { return b.version }
func (b *Base) Pending() []Event       { return b.pending }
func (b *Base) ClearPending()          { b.pending = nil }
func (b *Base) SetID(id uuid.UUID)     { b.id = id }
func (b *Base) SetVersion(v int)       { b.version = v }

// Raise applies event to self (via the owning aggregate's Apply), bumps the version, and
// queues the event for persistence. self must be the concrete aggregate that embeds this
// Base; callers pass it explicitly since Go embedding provides no self-type.
func (b *Base) Raise(self Aggregate, event Event) {
	self.Apply(event)
	b.version++
	b.pending = append(b.pending, event)
}
