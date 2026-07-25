// Package user is the simplest aggregate in the platform: Create/Rename/Archive only, no
// relationships of its own (other aggregates reference User by id — see plan §2). It's the
// aggregate every other aggregate type validates against for Creator/Gamemaster/Player
// existence (plan §4.3).
package user

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/user/events"
	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType re-exports events.AggregateType — see domain/universe.AggregateType for why.
const AggregateType = events.AggregateType

type User struct {
	eventsourcing.Base

	name     string
	archived bool
}

func (u *User) Name() string     { return u.name }
func (u *User) IsArchived() bool { return u.archived }

func New(name string) (*User, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	u := &User{}
	u.raise(&events.UserCreated{ID: uuid.New(), Name: name, OccurredAt: time.Now().UTC()})
	return u, nil
}

func (u *User) Rename(name string) error {
	if u.archived {
		return ErrArchived
	}
	if name == "" {
		return ErrNameRequired
	}
	if name == u.name {
		return nil
	}
	u.raise(&events.UserRenamed{Name: name, OccurredAt: time.Now().UTC()})
	return nil
}

// Archive is idempotent — see universe.Universe.Archive's doc comment for why.
func (u *User) Archive() error {
	if u.archived {
		return nil
	}
	u.raise(&events.UserArchived{OccurredAt: time.Now().UTC()})
	return nil
}

func (u *User) Apply(event eventsourcing.Event) {
	switch e := event.(type) {
	case *events.UserCreated:
		u.SetID(e.ID)
		u.name = e.Name
	case *events.UserRenamed:
		u.name = e.Name
	case *events.UserArchived:
		u.archived = true
	}
}

func (u *User) raise(event eventsourcing.Event) {
	u.Base.Raise(u, event)
}
