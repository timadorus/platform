package timadorus

import "testing"

// TestPotCost exercises the tiered Pot-increase cost formula directly — potCost is
// package-private, so this lives in an internal (white-box) test file, matching
// attribute_bonus_internal_test.go's own precedent for testing this package's private helpers
// without a full event-processing round trip. See
// docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md Decision 1 for the formula.
func TestPotCost(t *testing.T) {
	cases := []struct {
		name           string
		initial, target int
		want           int
	}{
		{"no change costs nothing", 50, 50, 0},
		{"entirely within the 1-point tier", 50, 60, 10},
		{"entirely within the 1-point tier up to the boundary", 50, 90, 40},
		{"crosses the boundary: split between both tiers", 85, 95, 30},
		{"starts exactly at the boundary", 90, 95, 25},
		{"entirely within the 5-point tier", 92, 96, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := potCost(tc.initial, tc.target); got != tc.want {
				t.Errorf("potCost(%d, %d) = %d, want %d", tc.initial, tc.target, got, tc.want)
			}
		})
	}
}
