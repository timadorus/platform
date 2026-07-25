// Package eventsourcing provides the storage-agnostic event-sourcing framework shared by
// every aggregate type: the Aggregate/Event contracts, the generic Repository, and the
// EventStore port that concrete infrastructure (internal/eventstore/postgres) implements.
package eventsourcing

// Event is a domain event raised by an aggregate. Concrete event types live in each
// aggregate's own <aggregate>/events package and carry only the payload data that isn't
// already present in the envelope (aggregate id, aggregate type, version).
type Event interface {
	// EventType returns the stable, versioned type string used for JSON (de)serialization
	// and registry lookup, e.g. "universe.created.v1".
	EventType() string
}
