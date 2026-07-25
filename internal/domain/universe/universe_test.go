package universe_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/universe"
)

func TestNew(t *testing.T) {
	t.Run("requires a name", func(t *testing.T) {
		_, err := universe.New("", []uuid.UUID{uuid.New()})
		if err != universe.ErrNameRequired {
			t.Fatalf("got %v, want ErrNameRequired", err)
		}
	})

	t.Run("requires at least one creator", func(t *testing.T) {
		_, err := universe.New("Forgotten Realms", nil)
		if err != universe.ErrCreatorsRequired {
			t.Fatalf("got %v, want ErrCreatorsRequired", err)
		}
	})

	t.Run("creates with a generated id and de-duplicated creators", func(t *testing.T) {
		creator := uuid.New()
		u, err := universe.New("Forgotten Realms", []uuid.UUID{creator, creator})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.AggregateID() == uuid.Nil {
			t.Fatal("expected a generated id")
		}
		if u.Name() != "Forgotten Realms" {
			t.Fatalf("got name %q", u.Name())
		}
		if !u.HasCreator(creator) {
			t.Fatal("expected creator to be present")
		}
		if got := u.Version(); got != 1 {
			t.Fatalf("got version %d, want 1", got)
		}
		if got := len(u.Pending()); got != 1 {
			t.Fatalf("got %d pending events, want 1", got)
		}
	})
}

func TestRename(t *testing.T) {
	u := mustNew(t)

	t.Run("rejects an empty name", func(t *testing.T) {
		if err := u.Rename(""); err != universe.ErrNameRequired {
			t.Fatalf("got %v, want ErrNameRequired", err)
		}
	})

	t.Run("is a no-op when unchanged", func(t *testing.T) {
		u.ClearPending()
		if err := u.Rename(u.Name()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := len(u.Pending()); got != 0 {
			t.Fatalf("got %d pending events, want 0", got)
		}
	})

	t.Run("renames and raises an event", func(t *testing.T) {
		u.ClearPending()
		if err := u.Rename("Eberron"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.Name() != "Eberron" {
			t.Fatalf("got name %q", u.Name())
		}
		if got := len(u.Pending()); got != 1 {
			t.Fatalf("got %d pending events, want 1", got)
		}
	})
}

func TestCreators(t *testing.T) {
	creator := uuid.New()
	u, err := universe.New("Forgotten Realms", []uuid.UUID{creator})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u.ClearPending()

	t.Run("cannot remove the last creator", func(t *testing.T) {
		if err := u.RemoveCreator(creator); err != universe.ErrLastCreator {
			t.Fatalf("got %v, want ErrLastCreator", err)
		}
		if !u.HasCreator(creator) {
			t.Fatal("expected creator to remain present")
		}
	})

	t.Run("removing an unknown creator fails", func(t *testing.T) {
		if err := u.RemoveCreator(uuid.New()); err != universe.ErrCreatorNotFound {
			t.Fatalf("got %v, want ErrCreatorNotFound", err)
		}
	})

	t.Run("can remove a creator once there are two", func(t *testing.T) {
		second := uuid.New()
		if err := u.AddCreator(second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := u.RemoveCreator(creator); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.HasCreator(creator) {
			t.Fatal("expected creator to be removed")
		}
		if !u.HasCreator(second) {
			t.Fatal("expected second creator to remain")
		}
	})

	t.Run("adding an existing creator is idempotent", func(t *testing.T) {
		u.ClearPending()
		existing := uuid.New()
		if err := u.AddCreator(existing); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		u.ClearPending()
		if err := u.AddCreator(existing); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := len(u.Pending()); got != 0 {
			t.Fatalf("got %d pending events, want 0", got)
		}
	})
}

func TestArchive(t *testing.T) {
	u := mustNew(t)
	u.ClearPending()

	t.Run("archiving rejects further mutation", func(t *testing.T) {
		if err := u.Archive(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !u.IsArchived() {
			t.Fatal("expected universe to be archived")
		}
		if err := u.Rename("New Name"); err != universe.ErrArchived {
			t.Fatalf("got %v, want ErrArchived", err)
		}
		if err := u.AddCreator(uuid.New()); err != universe.ErrArchived {
			t.Fatalf("got %v, want ErrArchived", err)
		}
	})

	t.Run("archiving twice is idempotent", func(t *testing.T) {
		u.ClearPending()
		if err := u.Archive(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := len(u.Pending()); got != 0 {
			t.Fatalf("got %d pending events, want 0 (idempotent no-op)", got)
		}
	})
}

func TestApplyReplay(t *testing.T) {
	creator := uuid.New()
	original, err := universe.New("Forgotten Realms", []uuid.UUID{creator})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := original.Rename("Eberron"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second := uuid.New()
	if err := original.AddCreator(second); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	replayed := &universe.Universe{}
	replayed.SetID(original.AggregateID())
	for _, event := range original.Pending() {
		replayed.Apply(event)
	}
	replayed.SetVersion(len(original.Pending()))

	if replayed.Name() != "Eberron" {
		t.Fatalf("got name %q after replay", replayed.Name())
	}
	if !replayed.HasCreator(creator) || !replayed.HasCreator(second) {
		t.Fatal("expected both creators present after replay")
	}
	if replayed.Version() != original.Version() {
		t.Fatalf("got version %d, want %d", replayed.Version(), original.Version())
	}
}

func mustNew(t *testing.T) *universe.Universe {
	t.Helper()
	u, err := universe.New("Forgotten Realms", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return u
}
