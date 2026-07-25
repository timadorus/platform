// Package universe is the reference aggregate for this codebase: it exercises the
// non-empty-collection invariant (Creators) and the archiving pattern (§4.6 of the design)
// that every other aggregate type repeats. Only this package and its events sub-package may
// be imported by command-api; projector and query-api import only the events sub-package.
package universe

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/universe/events"
	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType re-exports events.AggregateType for command-side callers that already
// import this package; infrastructure on the read side should import
// domain/universe/events directly (see that package's doc comment).
const AggregateType = events.AggregateType

type Universe struct {
	eventsourcing.Base

	name     string
	creators map[uuid.UUID]struct{}
	archived bool
}

func (u *Universe) Name() string                 { return u.name }
func (u *Universe) IsArchived() bool             { return u.archived }
func (u *Universe) HasCreator(id uuid.UUID) bool { _, ok := u.creators[id]; return ok }

// New constructs and creates a new Universe. Returns ErrNameRequired if name is blank or
// ErrCreatorsRequired if creatorIDs is empty (after de-duplication) — a Universe must
// always have at least one Creator.
func New(name string, creatorIDs []uuid.UUID) (*Universe, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	ids := dedupe(creatorIDs)
	if len(ids) == 0 {
		return nil, ErrCreatorsRequired
	}
	u := &Universe{}
	u.raise(&events.UniverseCreated{
		ID:             uuid.New(),
		Name:           name,
		CreatorUserIDs: ids,
		OccurredAt:     time.Now().UTC(),
	})
	return u, nil
}

// Rename changes the universe's display name. A no-op (no event raised) if name is
// unchanged.
func (u *Universe) Rename(name string) error {
	if u.archived {
		return ErrArchived
	}
	if name == "" {
		return ErrNameRequired
	}
	if name == u.name {
		return nil
	}
	u.raise(&events.UniverseRenamed{Name: name, OccurredAt: time.Now().UTC()})
	return nil
}

// AddCreator adds userID to the set of Creators. Idempotent: adding an existing Creator is
// a no-op.
func (u *Universe) AddCreator(userID uuid.UUID) error {
	if u.archived {
		return ErrArchived
	}
	if u.HasCreator(userID) {
		return nil
	}
	u.raise(&events.CreatorAdded{UserID: userID, OccurredAt: time.Now().UTC()})
	return nil
}

// RemoveCreator removes userID from the set of Creators. Rejects with ErrLastCreator if
// userID is the sole remaining Creator — a Universe must always have at least one.
func (u *Universe) RemoveCreator(userID uuid.UUID) error {
	if u.archived {
		return ErrArchived
	}
	if !u.HasCreator(userID) {
		return ErrCreatorNotFound
	}
	if len(u.creators) == 1 {
		return ErrLastCreator
	}
	u.raise(&events.CreatorRemoved{UserID: userID, OccurredAt: time.Now().UTC()})
	return nil
}

// Archive marks the universe archived. Idempotent: archiving an already-archived universe
// is a no-op, unlike every other mutating command which rejects once archived (§4.6 of the
// design: archiving is one-way and must always succeed).
func (u *Universe) Archive() error {
	if u.archived {
		return nil
	}
	u.raise(&events.UniverseArchived{OccurredAt: time.Now().UTC()})
	return nil
}

func (u *Universe) Apply(event eventsourcing.Event) {
	switch e := event.(type) {
	case *events.UniverseCreated:
		u.SetID(e.ID)
		u.name = e.Name
		u.creators = toSet(e.CreatorUserIDs)
	case *events.UniverseRenamed:
		u.name = e.Name
	case *events.CreatorAdded:
		u.creators[e.UserID] = struct{}{}
	case *events.CreatorRemoved:
		delete(u.creators, e.UserID)
	case *events.UniverseArchived:
		u.archived = true
	}
}

func (u *Universe) raise(event eventsourcing.Event) {
	u.Base.Raise(u, event)
}

func dedupe(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func toSet(ids []uuid.UUID) map[uuid.UUID]struct{} {
	set := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}
