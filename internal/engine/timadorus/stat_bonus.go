package timadorus

// StatBonus is one row of the bonuses table: the Bonus (and, for a future spell-point mechanic,
// the SpellPoint multiplier) attached to every stat value at or above minval, up to (but not
// including) the next row's own minval. Rows are ordered highest minval first — see bonuses' own
// doc comment for why that order matters.
type StatBonus struct {
	minval     int
	bonus      int
	spellPoint float32
}

// bonuses is the Timadorus ruleset's own stat-to-bonus threshold table, hardcoded here rather
// than read from a Ruleset's own data (traits.yaml's own table pattern, internal/engine/timadorus/tables)
// since it's a fixed rule of the "timadorus" ruleset itself, not Campaign- or
// Gamemaster-configurable data. Ordered from highest minval to lowest — GetStatBonus and
// GetSpellPointBonus both rely on this order, returning the first (i.e. highest) row whose minval
// the given stat still satisfies. A stat that falls between two defined thresholds (e.g. 86, with
// rows at 87 and 84) resolves to the next LOWER row's value (84's), not its own.
var bonuses = []StatBonus{
	{minval: 100, bonus: 25, spellPoint: 3.0},
	{minval: 99, bonus: 23, spellPoint: 2.8},
	{minval: 98, bonus: 21, spellPoint: 2.6},
	{minval: 97, bonus: 19, spellPoint: 2.4},
	{minval: 96, bonus: 17, spellPoint: 2.2},
	{minval: 95, bonus: 15, spellPoint: 2.0},
	{minval: 94, bonus: 14, spellPoint: 1.9},
	{minval: 93, bonus: 13, spellPoint: 1.8},
	{minval: 92, bonus: 12, spellPoint: 1.7},
	{minval: 91, bonus: 11, spellPoint: 1.6},
	{minval: 90, bonus: 10, spellPoint: 1.5},
	{minval: 87, bonus: 9, spellPoint: 1.4},
	{minval: 84, bonus: 8, spellPoint: 1.3},
	{minval: 81, bonus: 7, spellPoint: 1.2},
	{minval: 78, bonus: 6, spellPoint: 1.1},
	{minval: 75, bonus: 5, spellPoint: 1.0},
	{minval: 72, bonus: 4, spellPoint: 0.8},
	{minval: 68, bonus: 3, spellPoint: 0.6},
	{minval: 64, bonus: 2, spellPoint: 0.4},
	{minval: 60, bonus: 1, spellPoint: 0.2},
	{minval: 41, bonus: 0, spellPoint: 0.0},
	{minval: 37, bonus: -1, spellPoint: 0.0},
	{minval: 33, bonus: -2, spellPoint: 0.0},
	{minval: 29, bonus: -3, spellPoint: 0.0},
	{minval: 28, bonus: -4, spellPoint: 0.0},
	{minval: 25, bonus: -5, spellPoint: 0.0},
	{minval: 22, bonus: -6, spellPoint: 0.0},
	{minval: 19, bonus: -7, spellPoint: 0.0},
	{minval: 16, bonus: -8, spellPoint: 0.0},
	{minval: 12, bonus: -9, spellPoint: 0.0},
	{minval: 11, bonus: -10, spellPoint: 0.0},
	{minval: 10, bonus: -11, spellPoint: 0.0},
	{minval: 9, bonus: -12, spellPoint: 0.0},
	{minval: 8, bonus: -13, spellPoint: 0.0},
	{minval: 7, bonus: -14, spellPoint: 0.0},
	{minval: 6, bonus: -15, spellPoint: 0.0},
	{minval: 5, bonus: -17, spellPoint: 0.0},
	{minval: 4, bonus: -19, spellPoint: 0.0},
	{minval: 3, bonus: -21, spellPoint: 0.0},
	{minval: 2, bonus: -23, spellPoint: 0.0},
	{minval: 1, bonus: -25, spellPoint: 0.0},
}

// GetStatBonus returns the Bonus for a given stat value (Temp, in the "attributes" sense — see
// setAttributeTemp in character_processor.go, its one caller today), by scanning bonuses top-down
// for the first row whose minval the stat still satisfies. A stat below every row's minval (i.e.
// below 1) returns 0, the same as this loop's own natural zero-value fallback.
func GetStatBonus(stat int) int {
	for _, bonus := range bonuses {
		if stat >= bonus.minval {
			return bonus.bonus
		}
	}
	return 0
}

// GetSpellPointBonus returns the SpellPoint multiplier for a given stat value, via the same
// bonuses table and lookup as GetStatBonus. Not yet called from anywhere — reserved for a future
// spell-point mechanic; out of scope for
// docs/superpowers/specs/2026-09-11-stat-bonus-formula-design.md.
func GetSpellPointBonus(stat int) float32 {
	for _, bonus := range bonuses {
		if stat >= bonus.minval {
			return bonus.spellPoint
		}
	}
	return 0.0
}
