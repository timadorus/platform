package timadorus

import (
	"testing"

	"github.com/google/uuid"
)

func TestRulesetCache(t *testing.T) {
	c := newRulesetCache()
	campaignID := uuid.New()

	if _, ok := c.get(campaignID); ok {
		t.Fatal("got a hit on an empty cache, want a miss")
	}

	c.set(campaignID, "Timadorus")

	name, ok := c.get(campaignID)
	if !ok {
		t.Fatal("got a miss right after set, want a hit")
	}
	if name != "Timadorus" {
		t.Fatalf("got %q, want %q", name, "Timadorus")
	}

	// A second campaign must not collide with the first.
	other := uuid.New()
	if _, ok := c.get(other); ok {
		t.Fatal("got a hit for an unrelated campaign id, want a miss")
	}
}
