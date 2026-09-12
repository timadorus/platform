package timadorus_test

import (
	"testing"

	"github.com/timadorus/platform/internal/engine/timadorus"
)

// TestGetStatBonus locks in the bonuses table's current, corrected behavior — GetStatBonus is
// exported, so this needs no white-box test file. Every case here was verified by actually
// running the table's own logic (docs/superpowers/specs/2026-09-11-stat-bonus-formula-design.md),
// not hand-computed, since the table's row order and gaps make manual tracing error-prone.
func TestGetStatBonus(t *testing.T) {
	cases := []struct {
		name string
		stat int
		want int
	}{
		{"far above the table has no upper bound", 150, 25},
		{"top row", 100, 25},
		{"second row", 99, 23},
		{"the row-96/97 pair, no longer tied after the table fix", 97, 19},
		{"", 96, 17},
		{"falls through to the next LOWER row, not its own", 87, 9},
		{"", 86, 8},
		{"", 84, 8},
		{"top edge of the zero band", 60, 1},
		{"", 59, 0},
		{"the creation-time baseline this whole change exists to get right", 50, 0},
		{"zero band's own lower edge, after the 0/-1 boundary shift to restore ±1 symmetry", 42, 0},
		{"just below the zero band's new lower edge", 41, -1},
		{"", 40, -1},
		{"bottom row", 1, -25},
		{"below the table entirely — the function's own fallback", 0, 0},
		{"", -5, 0},
	}
	for _, tc := range cases {
		name := tc.name
		if name == "" {
			name = "boundary case"
		}
		t.Run(name, func(t *testing.T) {
			if got := timadorus.GetStatBonus(tc.stat); got != tc.want {
				t.Errorf("GetStatBonus(%d) = %d, want %d", tc.stat, got, tc.want)
			}
		})
	}
}
