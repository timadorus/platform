package rebuildreadmodels_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/projection/registry"
	"github.com/timadorus/platform/internal/rebuildreadmodels"
)

// seedEvents appends n events of aggregateType to the events table, each with its own aggregate
// id at version 1, so the events land at consecutive global_seq values in call order. Callers
// interleave calls to lay out a known per-aggregate-type distribution of global_seq.
func seedEvents(t *testing.T, pool *pgxpool.Pool, aggregateType string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO events (aggregate_id, aggregate_type, version, event_type, payload)
			 VALUES (gen_random_uuid(), $1, 1, $2, '{}')`,
			aggregateType, aggregateType+".created.v1",
		); err != nil {
			t.Fatalf("seed %s event %d: %v", aggregateType, i, err)
		}
	}
}

// seedUnequalWorkload writes an interleaved, deliberately UNEQUAL number of events per aggregate
// type and returns the resulting whole-table watermark alongside each base projector's own real
// target. The last event written is a 'user' event, so exactly one of the seven base projectors
// (user-read-model) can ever reach the whole-table maximum — which is the precise shape of the
// bug this file exists to guard: the tool used to hand that whole-table maximum to all seven
// projectors as their target, an unsatisfiable demand for the other six.
func seedUnequalWorkload(t *testing.T, pool *pgxpool.Pool) (watermark int64, wantTargets map[string]int64) {
	t.Helper()
	seedEvents(t, pool, "universe", 2)  // global_seq 1-2
	seedEvents(t, pool, "user", 5)      // 3-7
	seedEvents(t, pool, "campaign", 1)  // 8
	seedEvents(t, pool, "entity", 3)    // 9-11
	seedEvents(t, pool, "character", 4) // 12-15
	seedEvents(t, pool, "object", 2)    // 16-17
	seedEvents(t, pool, "ruleset", 6)   // 18-23
	seedEvents(t, pool, "user", 1)      // 24 — the newest event in the whole table

	return 24, map[string]int64{
		"universe-read-model":  2,
		"user-read-model":      24,
		"campaign-read-model":  8,
		"entity-read-model":    11,
		"character-read-model": 15,
		"object-read-model":    17,
		"ruleset-read-model":   23,
	}
}

func TestMaxGlobalSeqForAggregateType_UnequalCounts(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	watermark, _ := seedUnequalWorkload(t, pool)

	got, err := rebuildreadmodels.MaxGlobalSeq(ctx, pool)
	if err != nil {
		t.Fatalf("MaxGlobalSeq: %v", err)
	}
	if got != watermark {
		t.Fatalf("whole-table max is %d, want %d", got, watermark)
	}

	for aggregateType, wantSeq := range map[string]int64{
		"universe": 2, "user": 24, "campaign": 8, "entity": 11,
		"character": 15, "object": 17, "ruleset": 23,
	} {
		got, err := rebuildreadmodels.MaxGlobalSeqForAggregateType(ctx, pool, aggregateType, watermark)
		if err != nil {
			t.Fatalf("MaxGlobalSeqForAggregateType(%q): %v", aggregateType, err)
		}
		if got != wantSeq {
			t.Errorf("MaxGlobalSeqForAggregateType(%q) = %d, want %d", aggregateType, got, wantSeq)
		}
	}
}

func TestMaxGlobalSeqForAggregateType_BoundedByWatermark(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	seedUnequalWorkload(t, pool)

	// Bounding at 10 must ignore everything written afterwards, so a target captured at the start
	// of a rebuild cannot drift upward while projectors are still catching up.
	got, err := rebuildreadmodels.MaxGlobalSeqForAggregateType(ctx, pool, "user", 10)
	if err != nil {
		t.Fatalf("MaxGlobalSeqForAggregateType: %v", err)
	}
	if got != 7 {
		t.Fatalf("got %d, want 7 (the last 'user' event at or below global_seq=10)", got)
	}
}

func TestMaxGlobalSeqForAggregateType_NoEventsOfThatType_ReturnsZero(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	seedEvents(t, pool, "universe", 3)

	got, err := rebuildreadmodels.MaxGlobalSeqForAggregateType(ctx, pool, "ruleset", 3)
	if err != nil {
		t.Fatalf("MaxGlobalSeqForAggregateType: %v", err)
	}
	if got != 0 {
		t.Fatalf("got %d, want 0 for an aggregate type with no events yet", got)
	}
}

// TestComputeTargets_UnequalEventCountsPerAggregateType is the regression proof for the Critical
// finding: each real registered base projector's catch-up target must be its OWN aggregate type's
// maximum global_seq, not the whole-table maximum. Under the original logic all seven projectors
// were given the whole-table maximum (24 here), which six of them can never reach, so phase 1
// polled forever and phase 2's guard refused to proceed.
func TestComputeTargets_UnequalEventCountsPerAggregateType(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	watermark, want := seedUnequalWorkload(t, pool)

	base := registry.Base()
	targets, err := rebuildreadmodels.ComputeTargets(ctx, pool, base, watermark)
	if err != nil {
		t.Fatalf("ComputeTargets: %v", err)
	}
	if len(targets) != len(base) {
		t.Fatalf("got %d targets for %d projectors", len(targets), len(base))
	}

	belowWatermark := 0
	for _, got := range targets {
		wantSeq, ok := want[got.Name]
		if !ok {
			t.Errorf("unexpected projector %q in targets — update this test's expectations", got.Name)
			continue
		}
		if got.Target != wantSeq {
			t.Errorf("projector %q target = %d, want %d (its own aggregate type's max, not the whole-table max of %d)",
				got.Name, got.Target, wantSeq, watermark)
		}
		if got.Target < watermark {
			belowWatermark++
		}
	}

	// The whole point: with unequal counts, all but one projector's real target is strictly below
	// the whole-table watermark. If this ever came out as 0, the test would have degenerated back
	// into the old all-equal-counts assumption that hid the bug.
	if belowWatermark != len(base)-1 {
		t.Fatalf("%d of %d targets are below the watermark, want %d — the seeded workload must keep aggregate-type counts unequal",
			belowWatermark, len(base), len(base)-1)
	}
}

func TestComputeTargets_EmptyEventsTable_AllZero(t *testing.T) {
	pool := newTestPool(t)
	targets, err := rebuildreadmodels.ComputeTargets(context.Background(), pool, registry.Base(), 0)
	if err != nil {
		t.Fatalf("ComputeTargets: %v", err)
	}
	for _, got := range targets {
		if got.Target != 0 {
			t.Errorf("projector %q target = %d, want 0 on an empty events table", got.Name, got.Target)
		}
	}
}

// TestVerifyCaughtUp_PassesAtOwnTargets is phase 2's guard on the same unequal workload: it must
// pass once every base projector has reached its OWN target, even though six of the seven
// checkpoints are still below the whole-table watermark the operator passes as --target-seq.
// The checkpoints below are seeded from seedUnequalWorkload's independently-known expectations,
// never from ComputeTargets' own output — otherwise the buggy "everyone targets the watermark"
// computation would seed matching checkpoints and these tests would pass tautologically. Seeded
// this way they model the real thing: a projector that has genuinely consumed every event of its
// own aggregate type and can advance no further.
func seedCheckpointsAtOwnMaxima(t *testing.T, pool *pgxpool.Pool, checkpoints map[string]int64) {
	t.Helper()
	ctx := context.Background()
	for name, value := range checkpoints {
		if err := rebuildreadmodels.SetCheckpoint(ctx, pool, name, value); err != nil {
			t.Fatalf("seed checkpoint %q: %v", name, err)
		}
	}
}

func TestVerifyCaughtUp_PassesAtOwnTargets(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	watermark, caughtUp := seedUnequalWorkload(t, pool)
	seedCheckpointsAtOwnMaxima(t, pool, caughtUp)

	targets, err := rebuildreadmodels.ComputeTargets(ctx, pool, registry.Base(), watermark)
	if err != nil {
		t.Fatalf("ComputeTargets: %v", err)
	}
	if err := rebuildreadmodels.VerifyCaughtUp(ctx, pool, targets); err != nil {
		t.Fatalf("VerifyCaughtUp: %v — the guard must pass once each projector has consumed every event of its own aggregate type, even though six of the seven checkpoints are below the watermark of %d", err, watermark)
	}
}

func TestVerifyCaughtUp_RefusesWhenOneProjectorLags(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	watermark, caughtUp := seedUnequalWorkload(t, pool)
	caughtUp["ruleset-read-model"]-- // one event short of its own target
	seedCheckpointsAtOwnMaxima(t, pool, caughtUp)

	targets, err := rebuildreadmodels.ComputeTargets(ctx, pool, registry.Base(), watermark)
	if err != nil {
		t.Fatalf("ComputeTargets: %v", err)
	}
	err = rebuildreadmodels.VerifyCaughtUp(ctx, pool, targets)
	if err == nil {
		t.Fatal("VerifyCaughtUp returned nil, want an error naming the lagging projector")
	}
	if !strings.Contains(err.Error(), "ruleset-read-model") {
		t.Fatalf("error %q does not name the lagging projector", err)
	}
}

// TestWaitForCatchUp_PerProjectorTargets guards phase 1's polling against the same defect: a
// projector sitting at its own (below-watermark) target must count as caught up.
func TestWaitForCatchUp_PerProjectorTargets(t *testing.T) {
	pool := newTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	watermark, caughtUp := seedUnequalWorkload(t, pool)
	seedCheckpointsAtOwnMaxima(t, pool, caughtUp)

	targets, err := rebuildreadmodels.ComputeTargets(ctx, pool, registry.Base(), watermark)
	if err != nil {
		t.Fatalf("ComputeTargets: %v", err)
	}

	// A long interval: had every projector been handed the whole-table watermark as its target
	// (the original bug), six of them would never satisfy it and this would block until ctx's
	// 10s timeout, failing with context.DeadlineExceeded.
	if err := rebuildreadmodels.WaitForCatchUp(ctx, pool, targets, time.Minute, nil); err != nil {
		t.Fatalf("WaitForCatchUp: %v — every projector has consumed all of its own aggregate type's events, so this must return at once", err)
	}
}
