# Real Stat Bonus Formula Design

## Context

`character_processor.go`'s `defaultAttributes()` seeds each of a new Timadorus Character's ten
attributes with `{temp, pot, bonus}`. `bonus` has always been computed by a placeholder,
`attributeBonus(temp int) int` (`floor((temp-50)/10)`), explicitly documented as "standing in for
the real timadorus-engine rules formula, which is not yet specified" — see
[[character-attributes-design]] Decision 4.

That real formula now exists: `internal/engine/timadorus/stat_bonus.go` (added directly to the
working tree, not yet committed) defines `GetStatBonus(stat int) int`, a threshold table (`[]StatBonus{minval, bonus,
spellPoint}`) mapping a stat value to its bonus. This work retires the placeholder and wires
`GetStatBonus` in as the real formula, everywhere `attributeBonus` was.

Nothing in the codebase changes a Character's Temp after creation today — the sole existing site
that ever sets Temp is `defaultAttributes()` itself. Per the user, this stays true for now: Temp
will start changing through **future timadorus-engine functions**, not through any SPA-facing
action shipping in this work. This design's job is narrower than "recompute bonus on every temp
change" sounds: it establishes the one function every future temp-setter must call, and wires the
one call site that exists today (creation) through it — not to invent a temp-changing feature that
doesn't exist yet.

## Key Decisions

1. **`GetStatBonus` replaces `attributeBonus` everywhere, including at creation.** `attributeBonus`
   is deleted. `GetStatBonus(50)` (the baseline creation Temp) returns **-1**, not the placeholder's
   `0` — confirmed with the user as the intended new baseline, not a bug to work around. Every test
   asserting a freshly created Character's Bonus is `0` is updated to `-1`.

2. **One choke point for "Temp changed, so Bonus must be recomputed."** A new package-private
   helper,
   ```go
   // setAttributeTemp sets attr's own "temp" key and recomputes "bonus" from it via GetStatBonus
   // in the same call — the one function every timadorus-engine code path that changes a
   // Character's Temp must go through, so Bonus can never drift out of sync with Temp. Pot is
   // left untouched.
   func setAttributeTemp(attr map[string]any, temp int) {
       attr["temp"] = temp
       attr["bonus"] = GetStatBonus(temp)
   }
   ```
   is the only place `"bonus"` is ever written from a Temp value. `defaultAttributes()` becomes the
   first (and today, only) caller: it builds each attribute's `pot` key directly, then calls
   `setAttributeTemp(attr, initialAttributeValue)` to set `temp`/`bonus` together. A future
   temp-changing engine function (out of scope here — see Context) calls the same helper on an
   already-loaded Character's existing attribute map, exactly like `attributeBonusHook`
   (`trait_hooks.go`) already mutates `pot` in place on a loaded `stats` map.

3. **`stat_bonus.go`'s data table is taken as-is.** One row (`minval: 96, bonus: 19`) breaks the
   surrounding pattern of +2-per-step, but the user confirmed it's intentional. Tests lock in the
   table's current behavior, including that row, without alleging it's a bug.

4. **`GetSpellPointBonus` is untouched and unused.** Out of scope — nothing today computes or
   stores a spell-point value, and wiring it up isn't part of this request (YAGNI).

5. **`stat_bonus.go` gains doc comments, not logic changes.** It currently has zero comments,
   inconsistent with every other file in this package. Adds a short doc comment on `StatBonus`,
   `bonuses`, `GetStatBonus`, and `GetSpellPointBonus` (noting the latter is currently unused) —
   the table's values and `GetStatBonus`/`GetSpellPointBonus`'s logic are byte-for-byte unchanged.
   The file is also `git add`ed for the first time (it exists only as an untracked file today).

## Data / Component Changes Summary

- `internal/engine/timadorus/stat_bonus.go`: doc comments only; newly committed to git.
- `internal/engine/timadorus/character_processor.go`: remove `attributeBonus` (and the now-unused
  `"math"` import); add `setAttributeTemp`; update `defaultAttributes()` to call it; update the
  doc comments that describe the old placeholder.
- Remove `internal/engine/timadorus/attribute_bonus_internal_test.go` (tests a function that no
  longer exists).
- Add `internal/engine/timadorus/stat_bonus_test.go` (external test package — `GetStatBonus` is
  exported, so no white-box test file is needed): table-driven coverage of `GetStatBonus`,
  including the top/bottom of the table, the "in between two defined thresholds" case, the
  confirmed-intentional 96/97 tie, and `GetStatBonus(50) == -1`.
- Add `internal/engine/timadorus/set_attribute_temp_internal_test.go` (internal — `setAttributeTemp`
  is package-private): confirms it overwrites both `temp` and `bonus` together and leaves `pot`
  untouched.
- Update `TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStats`
  (`character_processor_test.go`): the per-attribute `bonus == 0` assertion becomes `== -1`.
- Update `test/e2e/e2e_test.go`'s "creating a Character seeds its default stats" `It`: the
  per-attribute `bonus` assertion becomes `-1` (real-cluster suite — not run in this sandbox, but
  kept accurate).

## Testing Strategy

- **Go unit** (`stat_bonus_test.go`): `GetStatBonus` table-driven over: `100→25` (top, and
  `150→25` to confirm no upper bound), `99→23`, `96→19` and `97→19` (the confirmed-intentional tie),
  `85→7` (falls through to the next lower defined threshold, 83, not its own row), `59→0` and
  `58→-1` (the zero/negative boundary), `50→-1` (today's actual creation-time input), `1→-25`
  (bottom row), `0→0` and `-5→0` (below the table entirely, the function's own fallback).
- **Go unit** (`set_attribute_temp_internal_test.go`): starting from an attribute map with a stale
  `bonus`, calling `setAttributeTemp` with a new `temp` overwrites both `temp` and `bonus`
  (recomputed via `GetStatBonus`) and leaves `pot` untouched.
- **Go integration** (existing `TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStats`,
  updated): a real CharacterCreated event still seeds all ten attributes at `temp:50`, `pot:50`,
  now `bonus:-1`.
- **Real-cluster e2e** (existing `test/e2e/e2e_test.go` `It`, updated, not run in this sandbox):
  same assertion against a real running cluster.

## Out of Scope

- Any actual mechanism that changes a Character's Temp after creation — per the user, that arrives
  through future timadorus-engine functions not yet specified. This design only makes sure that,
  whenever it arrives, there's already one obvious, correct function for it to call.
- `GetSpellPointBonus` and anything spell-point-related.
- Changing `stat_bonus.go`'s `bonuses` table data, including the confirmed-intentional row 96.
