package timadorus

import "testing"

// TestAttributeBonus exercises the placeholder bonus formula directly — attributeBonus is
// package-private (see defaultAttributes' doc comment for why it isn't exported), so this lives
// in an internal (white-box) test file, matching cache_test.go's own precedent for testing this
// package's private helpers without a full event-processing round trip.
func TestAttributeBonus(t *testing.T) {
	cases := []struct {
		temp int
		want int
	}{
		{temp: 50, want: 0},  // baseline: zero bonus, matches today's static "+0" display
		{temp: 60, want: 1},  // divides evenly
		{temp: 40, want: -1}, // divides evenly, negative
		{temp: 45, want: -1}, // floors toward negative infinity, not toward zero
		{temp: 55, want: 0},  // does not round up early
	}
	for _, tc := range cases {
		if got := attributeBonus(tc.temp); got != tc.want {
			t.Errorf("attributeBonus(%d) = %d, want %d", tc.temp, got, tc.want)
		}
	}
}
