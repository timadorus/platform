package realtimehub_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timadorus/platform/internal/realtimehub"
)

func TestHub_Broadcast_DeliversToMatchingClauseOnly(t *testing.T) {
	h := realtimehub.NewHub()

	matchingOut, unregisterMatching := h.Register([]realtimehub.WatchClause{{Type: "character", AggregateID: "char-1"}})
	defer unregisterMatching()
	otherOut, unregisterOther := h.Register([]realtimehub.WatchClause{{Type: "character", AggregateID: "char-2"}})
	defer unregisterOther()

	h.Broadcast(realtimehub.ResolvedEvent{
		Change:     realtimehub.Change{GlobalSeq: 1, AggregateType: "character", AggregateID: "char-1", EventType: "character.renamed.v1"},
		UniverseID: "u1",
	})

	select {
	case got := <-matchingOut:
		if got.AggregateID != "char-1" {
			t.Fatalf("got %+v, want AggregateID char-1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("matching client got nothing")
	}

	select {
	case got := <-otherOut:
		t.Fatalf("non-matching client got a delivery it shouldn't have: %+v", got)
	case <-time.After(100 * time.Millisecond):
		// expected: nothing
	}
}

func TestHub_Broadcast_UniverseListMatchesAnyUniverseEvent(t *testing.T) {
	h := realtimehub.NewHub()
	out, unregister := h.Register([]realtimehub.WatchClause{{Type: "universe"}})
	defer unregister()

	h.Broadcast(realtimehub.ResolvedEvent{
		Change: realtimehub.Change{GlobalSeq: 1, AggregateType: "universe", AggregateID: "u1", EventType: "universe.renamed.v1"},
	})

	select {
	case got := <-out:
		if got.AggregateID != "u1" {
			t.Fatalf("got %+v, want AggregateID u1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("bare universe clause got nothing, want it to match any universe event")
	}
}

func TestHub_Broadcast_CharacterCampaignScopeMatches(t *testing.T) {
	h := realtimehub.NewHub()
	out, unregister := h.Register([]realtimehub.WatchClause{{Type: "character", CampaignID: "camp-1"}})
	defer unregister()

	h.Broadcast(realtimehub.ResolvedEvent{
		Change:     realtimehub.Change{GlobalSeq: 1, AggregateType: "character", AggregateID: "char-99", EventType: "character.info_changed.v1"},
		UniverseID: "u1",
		CampaignID: "camp-1",
	})

	select {
	case got := <-out:
		if got.AggregateID != "char-99" {
			t.Fatalf("got %+v, want AggregateID char-99", got)
		}
	case <-time.After(time.Second):
		t.Fatal("campaign-scoped clause got nothing")
	}
}

// TestHub_Broadcast_SlowClientIsDisconnectedNotBlocking is the load-bearing proof for the
// backpressure policy: a client that never drains its buffer must not prevent delivery to any
// other, well-behaved client, and must eventually see its channel closed rather than hang forever.
func TestHub_Broadcast_SlowClientIsDisconnectedNotBlocking(t *testing.T) {
	h := realtimehub.NewHub()
	slowOut, unregisterSlow := h.Register([]realtimehub.WatchClause{{Type: "entity", UniverseID: "u1"}})
	defer unregisterSlow()
	healthyOut, unregisterHealthy := h.Register([]realtimehub.WatchClause{{Type: "entity", UniverseID: "u1"}})
	defer unregisterHealthy()

	for i := 0; i < 40; i++ { // comfortably past the 32-slot buffer
		h.Broadcast(realtimehub.ResolvedEvent{
			Change:     realtimehub.Change{GlobalSeq: int64(i), AggregateType: "entity", AggregateID: "e1", EventType: "entity.renamed.v1"},
			UniverseID: "u1",
		})
		select {
		case <-healthyOut:
		case <-time.After(time.Second):
			t.Fatalf("healthy client stalled on broadcast %d — a slow sibling must not block it", i)
		}
	}

	select {
	case _, ok := <-slowOut:
		if ok {
			t.Fatal("slow client's channel yielded a value instead of being closed — buffer overflow should disconnect it")
		}
	case <-time.After(time.Second):
		t.Fatal("slow client's channel was neither closed nor delivered to — want it disconnected")
	}
}

func TestHub_Unregister_SafeAfterBroadcastAlreadyDisconnected(t *testing.T) {
	h := realtimehub.NewHub()
	out, unregister := h.Register([]realtimehub.WatchClause{{Type: "entity", UniverseID: "u1"}})

	for i := 0; i < 40; i++ {
		h.Broadcast(realtimehub.ResolvedEvent{
			Change:     realtimehub.Change{GlobalSeq: int64(i), AggregateType: "entity", AggregateID: "e1", EventType: "entity.renamed.v1"},
			UniverseID: "u1",
		})
	}
	<-out // drain whatever's buffered; irrelevant to this test

	// Broadcast has already disconnected this client (its buffer overflowed above). Calling the
	// unregister function Register returned must not panic (double-close) or deadlock.
	unregister()
}

// TestHub_ConcurrentRegisterBroadcastUnregister_NoRaceNoDeadlock is the one test in this package
// that actually exercises Hub from more than one goroutine at a time — every other test above
// calls Register/Broadcast/receive step-by-step from the test's own single goroutine, so the race
// detector has nothing concurrent to observe there. This drives Register/unregister, Broadcast,
// and channel receives from many goroutines at once (including one client that never drains, to
// exercise the overflow/eviction path under real concurrency rather than single-threaded
// step-by-step), bounded to a fixed broadcast count and an overall timeout so it stays fast and
// deterministic in CI.
//
// Delivery counts are inherently racy against the hub's own eviction of a slow client, so this
// only asserts invariants that must hold regardless of interleaving: no panic, no deadlock within
// the timeout, and every well-behaved/continuously-draining client receives at least a
// sanity-checked minimum (i.e. more than zero) without ever blocking past a short per-receive
// timeout.
func TestHub_ConcurrentRegisterBroadcastUnregister_NoRaceNoDeadlock(t *testing.T) {
	h := realtimehub.NewHub()

	const (
		broadcastIterations = 500
		fastClientCount     = 4
		churnerCount        = 3
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	// Register every fast client (and the one slow client that never drains) up front, before any
	// concurrent churn/broadcast starts — so the "each fast client received something" assertion
	// below can't flake on a client that simply hadn't registered yet by the time the broadcaster
	// raced through its fixed iteration count.
	type registered struct {
		out        <-chan realtimehub.Change
		unregister func()
	}
	fastClients := make([]registered, fastClientCount)
	for i := range fastClients {
		out, unregister := h.Register([]realtimehub.WatchClause{{Type: "entity", UniverseID: "u1"}})
		fastClients[i] = registered{out: out, unregister: unregister}
	}
	_, unregisterSlow := h.Register([]realtimehub.WatchClause{{Type: "entity", UniverseID: "u1"}}) // registered but never read from

	// ready gates the broadcaster: each fast-client goroutine signals it immediately after
	// starting, before entering its receive loop. Without this, the broadcaster (a tight,
	// non-blocking 500-iteration loop) can race through its whole run — evicting every fast
	// client as if it were slow, since disconnect() drains a client's already-buffered values
	// before closing it — before the scheduler has even run a freshly spawned goroutine once.
	var ready sync.WaitGroup
	ready.Add(fastClientCount)

	counts := make([]int64, fastClientCount)
	for i, fc := range fastClients {
		wg.Add(1)
		go func(idx int, out <-chan realtimehub.Change, unregister func()) {
			defer wg.Done()
			defer unregister()
			ready.Done()
			for {
				select {
				case _, ok := <-out:
					if !ok {
						return
					}
					atomic.AddInt64(&counts[idx], 1)
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
					t.Errorf("fast client %d: no delivery within 1s — hub appears stuck", idx)
					return
				}
			}
		}(i, fc.out, fc.unregister)
	}

	// Churners: concurrently register a short-lived client, optionally receive once, and
	// unregister — repeatedly, for the whole run — to stress Register/unregister racing against
	// the live broadcaster and the fast/slow clients' own registry entries, not just delivery.
	for i := 0; i < churnerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				out, unregister := h.Register([]realtimehub.WatchClause{{Type: "entity", UniverseID: "u1"}})
				select {
				case <-out:
				case <-time.After(5 * time.Millisecond):
				case <-ctx.Done():
				}
				unregister()
			}
		}()
	}

	// Wait for every fast-client goroutine to be actively receiving (plus a short grace period for
	// the last one to actually reach its select, just past ready.Done()) before letting the
	// broadcaster loose.
	ready.Wait()
	time.Sleep(20 * time.Millisecond)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel() // tell every other goroutine broadcasting is done
		defer unregisterSlow()
		for i := 0; i < broadcastIterations; i++ {
			h.Broadcast(realtimehub.ResolvedEvent{
				Change:     realtimehub.Change{GlobalSeq: int64(i), AggregateType: "entity", AggregateID: "e1", EventType: "entity.renamed.v1"},
				UniverseID: "u1",
			})
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for concurrent goroutines to finish — possible deadlock")
	}

	for i, c := range counts {
		if c == 0 {
			t.Errorf("fast client %d received zero events; want at least some deliveries under concurrent load", i)
		}
	}
}
