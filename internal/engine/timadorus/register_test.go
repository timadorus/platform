package timadorus_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/ruleset"
	"github.com/timadorus/platform/internal/engine/timadorus"
)

func TestRegisterRuleset_CreatesOnFirstCall(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	id, err := timadorus.RegisterRuleset(ctx, pool)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if id == uuid.Nil {
		t.Fatal("got nil id")
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

	firstID, err := timadorus.RegisterRuleset(ctx, pool)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	secondID, err := timadorus.RegisterRuleset(ctx, pool)
	if err != nil {
		t.Fatalf("second register: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("got second-call id %s, want it to match the first call's id %s", secondID, firstID)
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

	_, err := timadorus.RegisterRuleset(ctx, pool)
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
