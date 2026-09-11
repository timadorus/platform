package timadorus

import "testing"

// TestSetAttributeTemp exercises setAttributeTemp directly — it's package-private, so this lives
// in an internal (white-box) test file, matching this package's existing precedent
// (pot_cost_internal_test.go) for testing private helpers without a full event-processing round
// trip.
func TestSetAttributeTemp(t *testing.T) {
	// bonus (99) is deliberately a stale/wrong starting value — setAttributeTemp must overwrite
	// it, not leave it alone or merely validate it.
	attr := map[string]any{"temp": 50, "pot": 60, "bonus": 99}

	setAttributeTemp(attr, 70)

	if attr["temp"] != 70 {
		t.Fatalf("got temp %v, want 70", attr["temp"])
	}
	// GetStatBonus(70) = 3 (verified in stat_bonus_test.go's own TestGetStatBonus table) —
	// hardcoded here rather than computed via GetStatBonus(70) again, so this test can't pass
	// merely because both sides made the same mistake.
	if attr["bonus"] != 3 {
		t.Fatalf("got bonus %v, want 3 (GetStatBonus(70))", attr["bonus"])
	}
	if attr["pot"] != 60 {
		t.Fatalf("got pot %v, want 60 (unchanged)", attr["pot"])
	}
}
