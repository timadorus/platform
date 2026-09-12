package realtimehub_test

import (
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
