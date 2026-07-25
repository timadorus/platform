package user_test

import (
	"testing"

	"github.com/timadorus/platform/internal/domain/user"
)

func TestNew(t *testing.T) {
	t.Run("requires a name", func(t *testing.T) {
		if _, err := user.New(""); err != user.ErrNameRequired {
			t.Fatalf("got %v, want ErrNameRequired", err)
		}
	})

	t.Run("creates with a generated id", func(t *testing.T) {
		u, err := user.New("Alice")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.Name() != "Alice" {
			t.Fatalf("got name %q", u.Name())
		}
		if u.Version() != 1 {
			t.Fatalf("got version %d, want 1", u.Version())
		}
	})
}

func TestRenameAndArchive(t *testing.T) {
	u, err := user.New("Alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u.ClearPending()

	if err := u.Rename(""); err != user.ErrNameRequired {
		t.Fatalf("got %v, want ErrNameRequired", err)
	}

	if err := u.Rename("Alice"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(u.Pending()); got != 0 {
		t.Fatalf("rename to same name should be a no-op, got %d pending events", got)
	}

	if err := u.Rename("Alicia"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Name() != "Alicia" {
		t.Fatalf("got name %q", u.Name())
	}

	if err := u.Archive(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !u.IsArchived() {
		t.Fatal("expected user to be archived")
	}
	if err := u.Archive(); err != nil {
		t.Fatalf("archiving twice should be idempotent, got error: %v", err)
	}
	if err := u.Rename("Should Fail"); err != user.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
}
