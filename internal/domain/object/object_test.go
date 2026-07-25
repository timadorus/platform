package object_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/object"
)

func TestNew(t *testing.T) {
	universeID := uuid.New()

	if _, err := object.New(universeID, ""); err != object.ErrNameRequired {
		t.Fatalf("got %v, want ErrNameRequired", err)
	}

	o, err := object.New(universeID, "Ring of Invisibility")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.UniverseID() != universeID {
		t.Fatalf("got universeID %s, want %s", o.UniverseID(), universeID)
	}
	if o.Name() != "Ring of Invisibility" {
		t.Fatalf("got name %q", o.Name())
	}
}

func TestRenameAndArchive(t *testing.T) {
	o, err := object.New(uuid.New(), "Ring of Invisibility")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	o.ClearPending()

	if err := o.Archive(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := o.Archive(); err != nil {
		t.Fatalf("archiving twice should be idempotent, got: %v", err)
	}
	if err := o.Rename("Fail"); err != object.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
}
