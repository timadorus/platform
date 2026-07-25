package character_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/character"
)

func TestNew(t *testing.T) {
	campaignID, entityID, playerID := uuid.New(), uuid.New(), uuid.New()

	t.Run("requires a name", func(t *testing.T) {
		_, err := character.New(campaignID, entityID, playerID, "")
		if err != character.ErrNameRequired {
			t.Fatalf("got %v, want ErrNameRequired", err)
		}
	})

	t.Run("requires a player", func(t *testing.T) {
		_, err := character.New(campaignID, entityID, uuid.Nil, "Elminster")
		if err != character.ErrPlayerRequired {
			t.Fatalf("got %v, want ErrPlayerRequired", err)
		}
	})

	t.Run("creates and records campaign/entity/player references", func(t *testing.T) {
		c, err := character.New(campaignID, entityID, playerID, "Elminster")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.CampaignID() != campaignID {
			t.Fatalf("got campaignID %s, want %s", c.CampaignID(), campaignID)
		}
		if c.EntityID() != entityID {
			t.Fatalf("got entityID %s, want %s", c.EntityID(), entityID)
		}
		if c.PlayerUserID() != playerID {
			t.Fatalf("got playerUserID %s, want %s", c.PlayerUserID(), playerID)
		}
	})
}

func TestSetPlayer(t *testing.T) {
	c, err := character.New(uuid.New(), uuid.New(), uuid.New(), "Elminster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.ClearPending()

	if err := c.SetPlayer(uuid.Nil); err != character.ErrPlayerRequired {
		t.Fatalf("got %v, want ErrPlayerRequired (no 'unset player' allowed)", err)
	}

	newPlayer := uuid.New()
	if err := c.SetPlayer(newPlayer); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.PlayerUserID() != newPlayer {
		t.Fatalf("got player %s, want %s", c.PlayerUserID(), newPlayer)
	}

	c.ClearPending()
	if err := c.SetPlayer(newPlayer); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(c.Pending()); got != 0 {
		t.Fatalf("reassigning to the same player should be a no-op, got %d pending events", got)
	}
}

func TestArchive(t *testing.T) {
	c, err := character.New(uuid.New(), uuid.New(), uuid.New(), "Elminster")
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
	if err := c.Rename("Fail"); err != character.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
	if err := c.SetPlayer(uuid.New()); err != character.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
}
