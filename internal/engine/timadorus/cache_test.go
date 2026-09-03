package timadorus

import (
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestRulesetCache(t *testing.T) {
	c := NewRulesetCache()
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

// TestRulesetCache_ConcurrentGetSet_Race drives RulesetCache.get/set directly from many
// goroutines with no DB round-trip and no processor in the way, unlike
// TestSharedRulesetCache_ConcurrentAccess (shared_cache_test.go), whose extra
// characters_read_model hop before its Character path reaches the cache reliably lets the
// Campaign path win first — so under `go test -race` that test alone almost never actually
// catches a RulesetCache locking regression (verified: 28 runs against a version of cache.go
// with RulesetCache.get/set's mutex calls deleted caught zero races). This test's only job is
// giving -race a target it can't fail to see: many goroutines genuinely racing on a handful of
// shared keys, no I/O in the way to serialize them by accident.
func TestRulesetCache_ConcurrentGetSet_Race(t *testing.T) {
	c := NewRulesetCache()
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := ids[i%len(ids)]
			c.set(id, "timadorus")
			_, _ = c.get(id)
		}(i)
	}
	wg.Wait()
}
