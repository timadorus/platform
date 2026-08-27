package timadorus_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/timadorus/platform/internal/domain/ruleset"
	"github.com/timadorus/platform/internal/engine/timadorus"
)

func TestRegisterRuleset_CreatesOnFirstCall(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	if err := timadorus.RegisterRuleset(ctx, pool); err != nil {
		t.Fatalf("register: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ruleset_names WHERE name = $1`, timadorus.TargetRulesetName).Scan(&count); err != nil {
		t.Fatalf("count ruleset_names: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d ruleset_names rows for %q, want 1", count, timadorus.TargetRulesetName)
	}
}

func TestRegisterRuleset_NoOpOnSecondCall(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	if err := timadorus.RegisterRuleset(ctx, pool); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := timadorus.RegisterRuleset(ctx, pool); err != nil {
		t.Fatalf("second register: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE event_type = $1`, "ruleset.created.v1").Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d ruleset.created.v1 events after two registrations, want 1", count)
	}
}

func TestRegisterRuleset_PropagatesRealError(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	pool.Close() // guarantees RegisterRuleset's own pool.Begin fails: a real error, not ErrNameAlreadyExists

	err := timadorus.RegisterRuleset(ctx, pool)
	if err == nil {
		t.Fatal("got nil error from a closed pool, want a real error")
	}
	if errors.Is(err, ruleset.ErrNameAlreadyExists) {
		t.Fatalf("got ErrNameAlreadyExists from a closed pool, want a genuine failure: %v", err)
	}
}

func TestTargetRulesetName_MatchesProcessorTarget(t *testing.T) {
	if !strings.EqualFold(timadorus.TargetRulesetName, "timadorus") {
		t.Fatalf("TargetRulesetName %q no longer matches the processors' target name",
			timadorus.TargetRulesetName)
	}
}
