package eventsourcing

import (
	"fmt"
	"sync"
)

// Registry maps an event-type string to a factory that produces a zero-value pointer to
// the concrete event struct, so infrastructure can unmarshal a persisted JSON payload back
// into the correct Go type during replay without knowing about domain packages upfront.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]func() Event
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]func() Event)}
}

// Register adds a factory for eventType. Aggregate event packages call this once (via a
// package-level Register(*Registry) function) during wiring in cmd/*/main.go.
func (r *Registry) Register(eventType string, factory func() Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[eventType] = factory
}

// New constructs a zero-value Event for eventType, ready to be populated by
// json.Unmarshal.
func (r *Registry) New(eventType string) (Event, error) {
	r.mu.RLock()
	factory, ok := r.factories[eventType]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("eventsourcing: unknown event type %q", eventType)
	}
	return factory(), nil
}
