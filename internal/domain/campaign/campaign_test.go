package campaign_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/campaign"
)

func TestNew(t *testing.T) {
	universeID := uuid.New()

	t.Run("requires a name", func(t *testing.T) {
		_, err := campaign.New(universeID, uuid.New(), "", []uuid.UUID{uuid.New()})
		if err != campaign.ErrNameRequired {
			t.Fatalf("got %v, want ErrNameRequired", err)
		}
	})

	t.Run("requires at least one gamemaster", func(t *testing.T) {
		_, err := campaign.New(universeID, uuid.New(), "Curse of Strahd", nil)
		if err != campaign.ErrGamemastersRequired {
			t.Fatalf("got %v, want ErrGamemastersRequired", err)
		}
	})

	t.Run("creates and records the parent universe", func(t *testing.T) {
		gm := uuid.New()
		c, err := campaign.New(universeID, uuid.New(), "Curse of Strahd", []uuid.UUID{gm})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.UniverseID() != universeID {
			t.Fatalf("got universeID %s, want %s", c.UniverseID(), universeID)
		}
		if !c.HasGamemaster(gm) {
			t.Fatal("expected gamemaster to be present")
		}
	})
}

func TestGamemasters(t *testing.T) {
	gm := uuid.New()
	c, err := campaign.New(uuid.New(), uuid.New(), "Curse of Strahd", []uuid.UUID{gm})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.ClearPending()

	if err := c.RemoveGamemaster(gm); err != campaign.ErrLastGamemaster {
		t.Fatalf("got %v, want ErrLastGamemaster", err)
	}

	second := uuid.New()
	if err := c.AddGamemaster(second); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := c.RemoveGamemaster(gm); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.HasGamemaster(gm) {
		t.Fatal("expected gamemaster to be removed")
	}
}

func TestArchive(t *testing.T) {
	c, err := campaign.New(uuid.New(), uuid.New(), "Curse of Strahd", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.ClearPending()

	if err := c.Archive(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := c.Archive(); err != nil {
		t.Fatalf("archiving twice should be idempotent, got: %v", err)
	}
	if err := c.Rename("Fail"); err != campaign.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
	if err := c.AddGamemaster(uuid.New()); err != campaign.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
}
