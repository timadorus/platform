# Character Attributes (Temp/Pot/Bonus) Design

## Context

The Character detail page's Stats tab currently renders `AttributesTable.vue` as a fully static
placeholder: ten hardcoded attributes (Strength/ST, Agility/AG, Constitution/CO, Quickness/QU,
Self Discipline/SD, Memory/ME, Reasoning/RE, Empathy/EM, Presence/PR, Intuition/IN), each always
showing `value: 75, bonus: '+0'` regardless of the actual Character. This work replaces that
placeholder with real, engine-managed values stored in the Character's `info` field, following the
same `info.stats.*` pattern already established for `traitPoints`/`traits`
([[character-traits-design]]) and for a Campaign's `characterCreation.maxStatBudget`
([[campaign-max-stat-budget-design]] — informal name for the earlier "Max Stat Budget" design).

Scope, matching every prior extension of this pattern: **Timadorus-ruleset Characters only**. A
Character on any other Ruleset gets no `stats.attributes`/`stats.statBudget` seeding at all — the
SPA falls back to a placeholder display for those (see Section 2).

## Key Decisions

1. **Data shape.** `info.stats.attributes` is an object keyed by attribute **abbreviation**
   (`"ST"`, `"AG"`, ...), each value `{ "temp": number, "pot": number, "bonus": number }`. `info.stats.statBudget`
   is a plain number, sibling to `traitPoints`/`traits`/`attributes` under `stats`. Full shape at
   creation:
   ```json
   {
     "stats": {
       "traitPoints": 2,
       "traits": [],
       "attributes": {
         "ST": { "temp": 50, "pot": 50, "bonus": 0 },
         "AG": { "temp": 50, "pot": 50, "bonus": 0 },
         "CO": { "temp": 50, "pot": 50, "bonus": 0 },
         "QU": { "temp": 50, "pot": 50, "bonus": 0 },
         "SD": { "temp": 50, "pot": 50, "bonus": 0 },
         "ME": { "temp": 50, "pot": 50, "bonus": 0 },
         "RE": { "temp": 50, "pot": 50, "bonus": 0 },
         "EM": { "temp": 50, "pot": 50, "bonus": 0 },
         "PR": { "temp": 50, "pot": 50, "bonus": 0 },
         "IN": { "temp": 50, "pot": 50, "bonus": 0 }
       },
       "statBudget": 35
     }
   }
   ```

2. **All seeded by the engine, in one write.** `handleCharacterCreated` (in
   `internal/engine/timadorus/character_processor.go`) already calls `mutateInfo` once to seed
   `traitPoints`/`traits`; this work extends that same closure to also set `attributes` and
   (conditionally) `statBudget` in the same `info["stats"] = map[string]any{...}` literal. No new
   write path, no clobbering risk between features.

3. **`statBudget` is engine-derived, never client-submitted.** The create-character command
   (`POST /campaigns/{campaignId}/characters`), its OpenAPI schema, the `CharacterCreated` domain
   event, and `character.New(...)` are all **untouched** by this work. Instead, `handleCharacterCreated`
   reads the Campaign's own `configuration` directly — the same
   `SELECT configuration FROM campaigns_read_model WHERE id = $1` query `tryAddTrait`'s
   `loadCampaignTraits` already runs for trait validation — and pulls
   `characterCreation.maxStatBudget` out of it itself, mirroring how the Ruleset name is already
   resolved server-side via `RulesetCache.resolve` rather than trusted from the request. This is a
   stronger form of the "the engine must not trust the SPA" principle established for `addTrait`:
   here the SPA is never even asked to supply the value.

   `loadCampaignTraits` and this new lookup are refactored into one shared
   `loadCampaignConfiguration(ctx, tx, campaignID) (map[string]any, error)` helper — parses the
   Campaign's `configuration` JSON once (best-effort: malformed or absent JSON yields an empty
   map, matching `mutateInfo`'s own "start fresh on malformed JSON" philosophy) — so there's a
   single DB read instead of two independent ones for a Character creation that needs both.

   If the Campaign's `configuration` has no `characterCreation.maxStatBudget` (not expected in
   practice for a Timadorus Campaign, since `CampaignCreated` always seeds a default of 35 — see
   the earlier Max Stat Budget design — but not treated as an error if it's ever missing),
   `stats.statBudget` is simply omitted from the seeded object entirely, while `attributes`,
   `traitPoints`, and `traits` are still seeded normally.

4. **Bonus formula.** `bonus = floor((temp - 50) / 10)`, computed by a small helper function.
   Since nothing yet lets Temp change after creation, this always evaluates to `0` for a freshly
   created Character — the formula is written generally (not hardcoded to `0`) so it stays correct
   once a future piece of work adds a way to modify Temp. This is an explicitly acknowledged
   placeholder for "the real timadorus-engine formula" (which is out of scope for this design to
   fully specify) — chosen because it reproduces today's static `+0` display exactly at the
   baseline Temp value, so nothing regresses visually before a real formula is substituted in.

5. **SPA display — column order and fallback.** `AttributesTable.vue`'s columns become
   Attribute | Abbr | **Temp** | **Pot** | Bonus (Temp replaces the old "Value" column and sits
   first per the request; Pot is new, inserted between Temp and Bonus; Bonus keeps its existing
   position as the last column). The component keeps its own hardcoded list of the ten
   `{name, abbr}` pairs (display metadata, not per-Character data) and receives a new
   `attributes: Record<string, { temp: number; pot: number; bonus: number }>` prop, computed and
   passed down by `CharacterDetailView.vue` exactly the way `traits`/`traitPoints` already are
   (parsed defensively from `character.info` with a try/catch, defaulting to `{}`). A row whose
   abbreviation is missing from the prop (non-Timadorus Character, or the engine hasn't caught up
   yet right after creation — the same async-settling window `traits`/`traitPoints` already
   tolerate) shows `—` in Temp/Pot/Bonus instead of `0`, so "not yet seeded" is visually distinct
   from "genuinely zero." Bonus is formatted with an explicit sign (`0` → `"+0"`, `2` → `"+2"`,
   `-1` → `"-1"`), matching today's static style.

## Testing Strategy

- **Go unit** (`internal/engine/timadorus/character_processor_test.go`): CharacterCreated on a
  Timadorus Campaign with a seeded `characterCreation.maxStatBudget` (e.g. 40) seeds all 10
  attribute keys at `{temp:50,pot:50,bonus:0}` plus `statBudget:40`; the same with no
  `characterCreation` object at all omits `statBudget` but still seeds `attributes`/`traitPoints`/
  `traits`; the existing non-matching-ruleset no-op test is confirmed to still cover
  attributes/statBudget (via its existing "no InfoChanged event at all" assertion); a small
  dedicated test for the bonus-formula helper across several Temp values (50→0, 60→1, 40→-1,
  45→-1, nailing down floor-toward-negative-infinity behavior for values that don't divide evenly).
- **Playwright e2e** (new `web/e2e/character-attributes.spec.ts`, mirroring
  `character-traits.spec.ts`'s seed/locator conventions): a Character with seeded
  `stats.attributes` renders all 10 rows with real Temp/Pot/Bonus values in the correct columns
  and correctly signed Bonus text; a Character with no `stats.attributes` shows `—` in every
  Temp/Pot/Bonus cell rather than `0` or a crash.
- **Real-cluster e2e** (`test/e2e/e2e_test.go`): extend the existing "creates one of each
  aggregate..." `It` (or add a small new one) to assert, once the engine catches up,
  `info.stats.attributes` has all 10 keys at `temp:50/pot:50/bonus:0` and `info.stats.statBudget`
  equals the Timadorus Campaign's actual `characterCreation.maxStatBudget` (35, the engine's own
  seeded default, since this test doesn't change it).

## Out of Scope

- Any UI for editing Temp/Pot (no such request was made — this is a read-only display of
  engine-managed values, same as `traitPoints`/`traits` before the `addTrait` control existed).
- The real timadorus-engine bonus formula — the linear placeholder in Decision 4 stands in for it
  until that formula is provided.
- Independent verification of a client-submitted `statBudget` — moot, since the SPA never submits
  one at all (Decision 3).
