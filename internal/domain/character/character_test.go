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
		if c.Info() != "" {
			t.Fatalf("got info %q, want empty — CreateCharacterRequest has no info field", c.Info())
		}
	})
}

func TestSetInfo(t *testing.T) {
	c, err := character.New(uuid.New(), uuid.New(), uuid.New(), "Elminster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.ClearPending()

	if err := c.SetInfo(`{"alignment":"chaotic good"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := c.Info(); got != `{"alignment":"chaotic good"}` {
		t.Fatalf("got info %q", got)
	}
	if got := len(c.Pending()); got != 1 {
		t.Fatalf("got %d pending events, want 1", got)
	}

	if err := c.SetInfo(""); err != nil {
		t.Fatalf("unexpected error setting info back to empty: %v", err)
	}
	if c.Info() != "" {
		t.Fatalf("got info %q, want empty", c.Info())
	}
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
	if err := c.SetInfo("{}"); err != character.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
	if err := c.RequestAction("{}"); err != character.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
}

func TestRequestAction(t *testing.T) {
	c, err := character.New(uuid.New(), uuid.New(), uuid.New(), "Elminster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.ClearPending()

	nameBefore, campaignBefore, entityBefore, playerBefore, infoBefore := c.Name(), c.CampaignID(), c.EntityID(), c.PlayerUserID(), c.Info()

	if err := c.RequestAction(`{"foo":"bar"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(c.Pending()); got != 1 {
		t.Fatalf("got %d pending events, want 1", got)
	}
	if c.Name() != nameBefore || c.CampaignID() != campaignBefore || c.EntityID() != entityBefore ||
		c.PlayerUserID() != playerBefore || c.Info() != infoBefore {
		t.Fatalf("RequestAction must not mutate any field, but at least one changed")
	}
}
