package entity_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/entity"
)

func TestNew(t *testing.T) {
	universeID := uuid.New()

	if _, err := entity.New(universeID, ""); err != entity.ErrNameRequired {
		t.Fatalf("got %v, want ErrNameRequired", err)
	}

	e, err := entity.New(universeID, "Excalibur")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.UniverseID() != universeID {
		t.Fatalf("got universeID %s, want %s", e.UniverseID(), universeID)
	}
	if e.Name() != "Excalibur" {
		t.Fatalf("got name %q", e.Name())
	}
}

func TestRenameAndArchive(t *testing.T) {
	e, err := entity.New(uuid.New(), "Excalibur")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e.ClearPending()

	if err := e.Archive(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := e.Archive(); err != nil {
		t.Fatalf("archiving twice should be idempotent, got: %v", err)
	}
	if err := e.Rename("Fail"); err != entity.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
}
