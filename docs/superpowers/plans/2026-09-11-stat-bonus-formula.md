# Real Stat Bonus Formula Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retire the placeholder `attributeBonus` formula and wire in the real one, `GetStatBonus` (`internal/engine/timadorus/stat_bonus.go`), through a single choke-point helper every future Temp-changing engine function must call.

**Architecture:** `stat_bonus.go` (currently untracked, already fixed by the user so `GetStatBonus(50) == 0`) gets doc comments and its own test, then gets committed. `character_processor.go` gains `setAttributeTemp(attr map[string]any, temp int)`, the one function that sets `"temp"` and recomputes `"bonus"` together; `defaultAttributes()` (today's only Temp-setter) becomes its first caller, replacing the deleted `attributeBonus`.

**Tech Stack:** Go 1.26, `internal/engine/timadorus` (tested via `testcontainers-go` against a real Postgres for integration tests; plain `go test` for the two new unit test files, no container needed).

## Global Constraints

- `GetStatBonus(50) == 0` (verified by actually running the table's current logic — see Task 1's step 2) — the real formula agrees with the placeholder's output at the one input that exists today, so **no existing test's expected values change**.
- Nothing in the codebase changes a Character's Temp after creation today, and this plan does not add such a mechanism — it only makes sure a future one (an engine-internal function, not an SPA-facing action, per the user) has one correct, obvious function to call.
- `stat_bonus.go`'s `bonuses` table data is final for this plan — no further edits to its values.
- `GetSpellPointBonus` stays unused — out of scope.
- Full design spec: `docs/superpowers/specs/2026-09-11-stat-bonus-formula-design.md`.

---

### Task 1: Document and test `stat_bonus.go`, commit it to git

**Files:**
- Modify: `internal/engine/timadorus/stat_bonus.go` (currently untracked — doc comments only, no logic/data changes)
- Test: `internal/engine/timadorus/stat_bonus_test.go` (new)

**Interfaces:**
- Consumes: nothing new.
- Produces: `GetStatBonus(stat int) int` (already exported, unchanged signature) — consumed by Task 2's `setAttributeTemp`.

- [ ] **Step 1: Add doc comments to `stat_bonus.go`**

Replace the full contents of `internal/engine/timadorus/stat_bonus.go` with (data rows unchanged from the file's current, already-corrected content — only comments are added):

```go
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
```

- [ ] **Step 2: Write the test**

Create `internal/engine/timadorus/stat_bonus_test.go`:

```go
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
		{"zero band's own lower edge — the exact gap the table fix closed", 41, 0},
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
```

- [ ] **Step 3: Run the test**

Run: `go test ./internal/engine/timadorus/... -run TestGetStatBonus -v`
Expected: PASS, all 15 subtests.

- [ ] **Step 4: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean (no errors/warnings).

- [ ] **Step 5: Commit — this is `stat_bonus.go`'s first-ever commit**

```bash
git add internal/engine/timadorus/stat_bonus.go internal/engine/timadorus/stat_bonus_test.go
git commit -m "feat(engine): document and test GetStatBonus, commit stat_bonus.go

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 2: Wire `GetStatBonus` into `defaultAttributes` via a shared `setAttributeTemp` helper

**Files:**
- Modify: `internal/engine/timadorus/character_processor.go`
- Remove: `internal/engine/timadorus/attribute_bonus_internal_test.go`
- Test: `internal/engine/timadorus/set_attribute_temp_internal_test.go` (new)

**Interfaces:**
- Consumes: `GetStatBonus(stat int) int` (Task 1).
- Produces: `setAttributeTemp(attr map[string]any, temp int)` (package-private) — the one function any future Temp-changing engine code must call. `defaultAttributes() map[string]any` keeps its existing signature and return shape (`{"ST": {"temp":.., "pot":.., "bonus":..}, ...}`), unchanged from every caller's point of view (`handleCharacterCreated`, `Reconciler`).

- [ ] **Step 1: Remove the now-superseded placeholder and its test**

Delete `internal/engine/timadorus/attribute_bonus_internal_test.go` entirely (it tests `attributeBonus`, which this task deletes).

In `internal/engine/timadorus/character_processor.go`, remove the `"math"` import (it becomes unused once `attributeBonus` is deleted — nothing else in this file calls `math.*`):

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"
	...
```

becomes:

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	...
```

- [ ] **Step 2: Write the failing test**

Create `internal/engine/timadorus/set_attribute_temp_internal_test.go`:

```go
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
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/engine/timadorus/... -run TestSetAttributeTemp -v`
Expected: FAIL — build error, `undefined: setAttributeTemp` (also: the package won't build at all right now anyway, since Step 1 already removed `attributeBonus` without replacing it yet — that's expected and fine, this step's job is just to confirm the test is wired up before the implementation exists).

- [ ] **Step 4: Implement `setAttributeTemp` and wire it into `defaultAttributes`**

Replace:

```go
// attributeBonus computes an attribute's Bonus from its current Temp value. This is a
// placeholder formula (floor((temp-50)/10), zero at the baseline Temp of 50) standing in for the
// real timadorus-engine rules formula, which is not yet specified — see design spec "Character
// Attributes (Temp/Pot/Bonus)", Decision 4. Written generally (not hardcoded to 0) so it stays
// correct once something other than character creation can change Temp.
func attributeBonus(temp int) int {
	return int(math.Floor(float64(temp-50) / 10))
}
```

with:

```go
// setAttributeTemp sets attr's own "temp" key and recomputes "bonus" from it via GetStatBonus
// (stat_bonus.go) in the same call — the one function every timadorus-engine code path that
// changes a Character's Temp must go through, so Bonus can never drift out of sync with Temp.
// Pot is left untouched. defaultAttributes (below) is this function's first caller, seeding a
// freshly created Character's own baseline Temp; a future engine function that changes Temp on
// an already-loaded Character calls it the same way tryAddTrait's attributeBonusHook
// (trait_hooks.go) already mutates an existing attribute map's "pot" in place.
func setAttributeTemp(attr map[string]any, temp int) {
	attr["temp"] = temp
	attr["bonus"] = GetStatBonus(temp)
}
```

Then replace:

```go
// defaultAttributes builds the starting attributes object for a newly created
// "timadorus"-ruleset Character: all ten of the engine's own hardcoded attributes, Temp and Pot
// both at initialAttributeValue, Bonus computed from Temp via attributeBonus.
func defaultAttributes() map[string]any {
	attrs := make(map[string]any, len(attributeAbbreviations))
	for _, abbr := range attributeAbbreviations {
		attrs[abbr] = map[string]any{
			"temp":  initialAttributeValue,
			"pot":   initialAttributeValue,
			"bonus": attributeBonus(initialAttributeValue),
		}
	}
	return attrs
}
```

with:

```go
// defaultAttributes builds the starting attributes object for a newly created
// "timadorus"-ruleset Character: all ten of the engine's own hardcoded attributes, Pot at
// initialAttributeValue, Temp and Bonus set together via setAttributeTemp.
func defaultAttributes() map[string]any {
	attrs := make(map[string]any, len(attributeAbbreviations))
	for _, abbr := range attributeAbbreviations {
		attr := map[string]any{"pot": initialAttributeValue}
		setAttributeTemp(attr, initialAttributeValue)
		attrs[abbr] = attr
	}
	return attrs
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/engine/timadorus/... -run TestSetAttributeTemp -v`
Expected: PASS.

- [ ] **Step 6: Run the full package test suite — confirms the existing creation-flow tests need no changes**

Run: `go test ./internal/engine/timadorus/... -count=1`
Expected: PASS, including (unmodified) `TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStats`, which already asserts `bonus == 0` at creation and needs no edit (Global Constraints). Requires Docker running (`newTestPool` starts a real Postgres testcontainer) — confirm Docker is available first if this fails to start a container.

- [ ] **Step 7: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add internal/engine/timadorus/character_processor.go internal/engine/timadorus/set_attribute_temp_internal_test.go
git rm internal/engine/timadorus/attribute_bonus_internal_test.go
git commit -m "feat(engine): wire GetStatBonus into defaultAttributes via setAttributeTemp

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```
