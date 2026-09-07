# Assign Stats Budget (Pot Editing) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a player spend a Timadorus Character's `stats.statBudget` on raising one or more attributes' Pot values, via a modal on the Character detail page's Stats tab, with the timadorus-engine as the sole authority on cost and eligibility.

**Architecture:** A new presentational `AssignStatsBudgetModal.vue` (built on the existing `BaseModal.vue`) is opened from a new button in `AttributesTable.vue`, which owns the actual `PUT /characters/{id}/action` request and the existing "submit → pending → watch for confirmation → timeout" pattern already used by `BaseInfoTable.vue` (Add Trait) and `ConfigurationPanel.vue` (Max Stat Budget). Server-side, a new `trySubmitPot` in `internal/engine/timadorus/character_processor.go`, parallel to the existing `tryAddTrait`, independently recomputes the tiered cost from the Character's own stored Pot/statBudget and rejects the whole batch atomically on any violation.

**Tech Stack:** Vue 3 (`<script setup>`, TypeScript), Playwright e2e (no component-level unit test runner exists in this repo — `web/package.json` only has `test:e2e`), Go 1.26 (`internal/engine/timadorus`, tested via `testcontainers-go` against a real Postgres).

## Global Constraints

- Cost formula: for one attribute rising from `initial` to `target`, cost = `max(0, min(target,90)-initial)*1 + max(0, target-max(initial,90))*5`. Implemented identically in Go (`potCost`) and TypeScript (`potCost`) — see design spec Decision 1.
- Edit rules: a submitted Pot can never be below that attribute's own current stored Pot, never above `100`, and a batch's total cost can never exceed the Character's current stored `statBudget`. Any violation rejects the **entire batch** (design spec Decision 4).
- Action payload: `PUT /characters/{characterId}/action` body `{"action":"submitPot","pot":{"<abbr>":<number>,...}}`, always all 10 abbreviations (design spec Decision 8).
- Field names: `info.stats.statBudget` (number), `info.stats.attributes[abbr].pot` (number) — existing shape from [[character-attributes-design]], unchanged by this work except that `pot`/`statBudget` now become writable via `submitPot`.
- Pending/timeout convention: 10000ms (`PENDING_TIMEOUT_MS`/`ADD_TRAIT_TIMEOUT_MS`'s existing sibling value), pending text `"Update requested — refreshing…"`, timeout error text starting `"No confirmation received"` — matches `ConfigurationPanel.vue`/`BaseInfoTable.vue` exactly.
- Explanation text (verbatim): `"Set potential values. Pot ≤ 90 equals 1 budget point per attribute point. 91-100 cost 5 budget points per attribute point."`
- Full design spec: `docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md`.

---

### Task 1: Go — `potCost` cost-formula helper

**Files:**
- Modify: `internal/engine/timadorus/character_processor.go` (add `potCost`, near `attributeBonus`)
- Test: `internal/engine/timadorus/pot_cost_internal_test.go` (new)

**Interfaces:**
- Produces: `potCost(initial, target int) int` (package-private, in `package timadorus`) — used by Task 2's `trySubmitPot`.

- [ ] **Step 1: Write the failing test**

Create `internal/engine/timadorus/pot_cost_internal_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/timadorus/... -run TestPotCost -v`
Expected: FAIL — build error, `undefined: potCost`.

- [ ] **Step 3: Write minimal implementation**

In `internal/engine/timadorus/character_processor.go`, add this function directly after `attributeBonus` (after its closing `}`, before `defaultAttributes`):

```go
// potCost computes the statBudget cost of raising a single attribute's Pot from initial to
// target, per the tiered rule from docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md
// Decision 1: the portion of the increase at or below Pot 90 costs 1 statBudget point per Pot
// point; the portion above Pot 90 costs 5 statBudget points per Pot point. Callers must ensure
// target >= initial themselves (trySubmitPot rejects a decrease before ever calling this) — for
// target < initial this still returns 0 (both tiers clamp negative contributions to zero), it
// just isn't a meaningful "cost" in that case.
func potCost(initial, target int) int {
	below := min(target, 90) - initial
	if below < 0 {
		below = 0
	}
	above := target - max(initial, 90)
	if above < 0 {
		above = 0
	}
	return below + above*5
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/engine/timadorus/... -run TestPotCost -v`
Expected: PASS (all 6 subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/timadorus/character_processor.go internal/engine/timadorus/pot_cost_internal_test.go
git commit -m "feat(engine): add potCost tiered Pot-increase cost formula

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 2: Go — `trySubmitPot` action handling

**Files:**
- Modify: `internal/engine/timadorus/character_processor.go`
- Test: `internal/engine/timadorus/character_processor_test.go`

**Interfaces:**
- Consumes: `potCost(initial, target int) int` (Task 1).
- Produces: recognizes `{"action":"submitPot","pot":{"<abbr>":<number>,...}}` on `PUT /characters/{id}/action`, applying it via the same `SetInfo`/`Save` path `tryAddTrait` uses. No new exported symbols — `trySubmitPot` stays package-private, exercised only through the full event-processing round trip in `character_processor_test.go` (`package timadorus_test`).

- [ ] **Step 1: Write the failing tests**

Append to `internal/engine/timadorus/character_processor_test.go` (after `TestCharacterProcessor_AddTrait_Archived_RejectedAndLogged`, i.e. at the end of the file):

```go
// TestCharacterProcessor_SubmitPot_ValidBatch_Succeeds covers the happy path from
// docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md: a batch with one increased
// attribute (ST: 50->60, costing 10) and one unchanged attribute (AG: 50->50, costing 0) is
// applied atomically, decrementing statBudget by the total cost and leaving every other field
// (including AG's own pot) untouched.
func TestCharacterProcessor_SubmitPot_ValidBatch_Succeeds(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID,
		`{"stats":{"traitPoints":2,"traits":[],"attributes":{"ST":{"temp":50,"pot":50,"bonus":0},"AG":{"temp":50,"pot":50,"bonus":0}},"statBudget":40}}`)

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload: mustMarshal(t, events.ActionRequested{
			Payload: `{"action":"submitPot","pot":{"ST":60,"AG":50}}`, OccurredAt: time.Now().UTC(),
		}),
	})

	// seedCharacterInfo's own SetInfo (version 2) already leaves one character.info_changed.v1
	// event in place before the router ever runs — require version > 2, matching
	// TestCharacterProcessor_AddTrait_ValidTrait_Succeeds's own identical reasoning.
	deadline := time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2 AND version > 2
			 ORDER BY version DESC LIMIT 1`,
			characterID, events.TypeInfoChanged,
		).Scan(&payload)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query info_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if payload == nil {
		t.Fatal("timed out waiting for a character.info_changed.v1 event")
	}

	var infoEvent struct {
		Info string `json:"info"`
	}
	if err := json.Unmarshal(payload, &infoEvent); err != nil {
		t.Fatalf("info_changed payload %s is not the expected shape: %v", payload, err)
	}
	var decoded struct {
		Stats struct {
			StatBudget int `json:"statBudget"`
			Attributes map[string]struct {
				Pot int `json:"pot"`
			} `json:"attributes"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(infoEvent.Info), &decoded); err != nil {
		t.Fatalf("info %q is not the expected shape: %v", infoEvent.Info, err)
	}
	if decoded.Stats.StatBudget != 30 {
		t.Fatalf("got statBudget %d, want 30 (40 - potCost(50,60)=10)", decoded.Stats.StatBudget)
	}
	if decoded.Stats.Attributes["ST"].Pot != 60 {
		t.Fatalf("got ST pot %d, want 60", decoded.Stats.Attributes["ST"].Pot)
	}
	if decoded.Stats.Attributes["AG"].Pot != 50 {
		t.Fatalf("got AG pot %d, want 50 (unchanged)", decoded.Stats.Attributes["AG"].Pot)
	}

	wait()
}

// submitPotRejectionCase is shared by every TestCharacterProcessor_SubmitPot_*_RejectedAndLogged
// test below: seed a Character with seededInfo, publish the given submitPot payload, then assert
// no additional character.info_changed.v1 event was appended (the seeded info's own SetInfo
// already leaves exactly one) and that logs contains wantLogSubstring.
func runSubmitPotRejectionCase(t *testing.T, seededInfo, payload, wantLogSubstring string) {
	t.Helper()
	pool := newTestPool(t)
	logger, logs := newTestLogger()

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, seededInfo)

	p := timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	body := mustMarshal(t, bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload:   mustMarshal(t, events.ActionRequested{Payload: payload, OccurredAt: time.Now().UTC()}),
	})
	msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
	if err := inMemory.Publish(bus.Subject(events.AggregateType), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d character.info_changed.v1 events, want 1 (rejected, so no additional mutation)", count)
	}
	if !strings.Contains(logs.String(), wantLogSubstring) {
		t.Fatalf("expected a log line containing %q, got: %s", wantLogSubstring, logs.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

func TestCharacterProcessor_SubmitPot_InsufficientBudget_RejectedAndLogged(t *testing.T) {
	// ST 50->60 costs 10, but statBudget is only 5 — the whole batch is rejected, not partially
	// applied.
	runSubmitPotRejectionCase(t,
		`{"stats":{"attributes":{"ST":{"temp":50,"pot":50,"bonus":0}},"statBudget":5}}`,
		`{"action":"submitPot","pot":{"ST":60}}`,
		"statBudget",
	)
}

func TestCharacterProcessor_SubmitPot_Decrease_RejectedAndLogged(t *testing.T) {
	runSubmitPotRejectionCase(t,
		`{"stats":{"attributes":{"ST":{"temp":50,"pot":50,"bonus":0}},"statBudget":40}}`,
		`{"action":"submitPot","pot":{"ST":40}}`,
		"below",
	)
}

func TestCharacterProcessor_SubmitPot_Over100_RejectedAndLogged(t *testing.T) {
	runSubmitPotRejectionCase(t,
		`{"stats":{"attributes":{"ST":{"temp":50,"pot":50,"bonus":0}},"statBudget":9999}}`,
		`{"action":"submitPot","pot":{"ST":101}}`,
		"100",
	)
}

func TestCharacterProcessor_SubmitPot_UnknownAbbreviation_RejectedAndLogged(t *testing.T) {
	runSubmitPotRejectionCase(t,
		`{"stats":{"attributes":{"ST":{"temp":50,"pot":50,"bonus":0}},"statBudget":40}}`,
		`{"action":"submitPot","pot":{"ZZ":60}}`,
		"unknown",
	)
}

func TestCharacterProcessor_SubmitPot_NoStatBudgetSeeded_RejectedAndLogged(t *testing.T) {
	// A non-Timadorus Character, or one the engine hasn't finished seeding yet — no
	// stats.statBudget at all, matching tryAddTrait's own "no traitPoints field" treatment.
	runSubmitPotRejectionCase(t,
		`{"stats":{"attributes":{"ST":{"temp":50,"pot":50,"bonus":0}}}}`,
		`{"action":"submitPot","pot":{"ST":60}}`,
		"statBudget",
	)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/engine/timadorus/... -run TestCharacterProcessor_SubmitPot -v`
Expected: FAIL. `ValidBatch_Succeeds` times out waiting for a new `info_changed` event (the payload falls through to `appendActionTimestamp`'s generic path today, which does append one event, but a decode of the resulting `Info` as `{stats:{statBudget,attributes}}` will show the original unmodified statBudget of 40, not 30, so the assertion fails). The five rejection tests each fail their `count != 1` check: today's fallback path always appends an `actions` timestamp, producing a *second* `info_changed` event (`count == 2`), not the expected `1`.

- [ ] **Step 3: Write the implementation**

In `internal/engine/timadorus/character_processor.go`, replace the `characterAction` struct:

```go
type characterAction struct {
	Action string `json:"action"`
	Trait  string `json:"trait"`
}
```

with:

```go
type characterAction struct {
	Action string             `json:"action"`
	Trait  string             `json:"trait"`
	Pot    map[string]float64 `json:"pot"`
}
```

Replace the dispatch in `handleActionRequested`:

```go
	var action characterAction
	if err := json.Unmarshal([]byte(e.Payload), &action); err == nil && action.Action == "addTrait" {
		return p.tryAddTrait(ctx, tx, env, campaignID, action.Trait)
	}

	return p.appendActionTimestamp(ctx, tx, env, e.OccurredAt)
```

with:

```go
	var action characterAction
	if err := json.Unmarshal([]byte(e.Payload), &action); err == nil {
		switch action.Action {
		case "addTrait":
			return p.tryAddTrait(ctx, tx, env, campaignID, action.Trait)
		case "submitPot":
			return p.trySubmitPot(ctx, tx, env, action.Pot)
		}
	}

	return p.appendActionTimestamp(ctx, tx, env, e.OccurredAt)
```

Add `trySubmitPot` directly after `tryAddTrait`'s closing `}` (before `containsString`):

```go
// trySubmitPot validates a batch of target Pot values against the Character's own current
// stored attributes/statBudget before applying any of them — the engine, never the SPA, computes
// and checks the cost (see potCost and
// docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md). Any single invalid entry (an
// abbreviation not among the Character's existing attributes, a decrease, or a target over 100)
// or a total cost exceeding the current statBudget rejects the WHOLE batch — logged, not silent,
// exactly like tryAddTrait's own rejections — leaving every attribute and statBudget completely
// untouched. Deliberately NOT built on mutateInfo for the same reason tryAddTrait isn't: this
// mutation is conditional, and mutateInfo's contract is unconditional.
func (p *CharacterProcessor) trySubmitPot(ctx context.Context, tx pgx.Tx, env bus.Envelope, pot map[string]float64) error {
	characterID := env.AggregateID
	txCtx := postgres.WithTx(observability.WithCorrelationID(ctx, env.CorrelationID()), tx)

	c, err := p.characters.Load(txCtx, characterID)
	if err != nil {
		return fmt.Errorf(errPrefix+"load character %s: %w", characterID, err)
	}

	info := map[string]any{}
	if raw := c.Info(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &info) // best-effort; info stays {} on failure
	}
	stats, _ := info["stats"].(map[string]any)
	if stats == nil {
		p.logger.Warn("submitPot rejected: Character has no stats seeded yet", "characterID", characterID)
		return nil
	}
	attributes, _ := stats["attributes"].(map[string]any)
	if attributes == nil {
		p.logger.Warn("submitPot rejected: Character has no attributes seeded yet", "characterID", characterID)
		return nil
	}
	statBudget, ok := stats["statBudget"].(float64)
	if !ok {
		p.logger.Warn("submitPot rejected: Character has no statBudget", "characterID", characterID)
		return nil
	}

	totalCost := 0
	for abbr, targetVal := range pot {
		attr, ok := attributes[abbr].(map[string]any)
		if !ok {
			p.logger.Warn("submitPot rejected: unknown attribute abbreviation",
				"characterID", characterID, "abbr", abbr)
			return nil
		}
		currentPot, ok := attr["pot"].(float64)
		if !ok {
			p.logger.Warn("submitPot rejected: attribute has no pot value",
				"characterID", characterID, "abbr", abbr)
			return nil
		}
		target, current := int(targetVal), int(currentPot)
		if target < current {
			p.logger.Warn("submitPot rejected: target pot is below the attribute's current pot",
				"characterID", characterID, "abbr", abbr, "current", current, "target", target)
			return nil
		}
		if target > 100 {
			p.logger.Warn("submitPot rejected: target pot exceeds 100",
				"characterID", characterID, "abbr", abbr, "target", target)
			return nil
		}
		totalCost += potCost(current, target)
	}

	if totalCost > int(statBudget) {
		p.logger.Warn("submitPot rejected: total cost exceeds remaining statBudget",
			"characterID", characterID, "cost", totalCost, "statBudget", statBudget)
		return nil
	}

	for abbr, targetVal := range pot {
		attr, _ := attributes[abbr].(map[string]any)
		attr["pot"] = int(targetVal)
	}
	stats["statBudget"] = int(statBudget) - totalCost
	info["stats"] = stats

	newInfo, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf(errPrefix+"marshal updated info for character %s: %w", characterID, err)
	}
	if err := c.SetInfo(string(newInfo)); err != nil {
		if errors.Is(err, character.ErrArchived) {
			p.logger.Warn("submitPot rejected: Character is archived", "characterID", characterID)
			return nil
		}
		return fmt.Errorf(errPrefix+"set info for character %s: %w", characterID, err)
	}
	if err := p.characters.Save(txCtx, c); err != nil {
		return fmt.Errorf(errPrefix+"save character %s: %w", characterID, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/engine/timadorus/... -run 'TestCharacterProcessor_SubmitPot|TestCharacterProcessor_AddTrait|TestCharacterProcessor_MatchingRuleset|TestCharacterProcessor_NonMatchingRuleset' -v`
Expected: PASS for every test (the new `SubmitPot` ones, and every pre-existing `AddTrait`/ruleset test — confirming the dispatch change didn't regress `addTrait` or the timestamp fallback).

- [ ] **Step 5: Run the full package test suite**

Run: `go test ./internal/engine/timadorus/... -v`
Expected: PASS. (Requires Docker running — `newTestPool` starts a real Postgres testcontainer.)

- [ ] **Step 6: Commit**

```bash
git add internal/engine/timadorus/character_processor.go internal/engine/timadorus/character_processor_test.go
git commit -m "feat(engine): recognize submitPot action, validating and applying Pot batches

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 3: Frontend — shared attribute metadata + `potCost`, refactor `AttributesTable.vue` to use it

**Files:**
- Create: `web/src/lib/attributes.ts`
- Modify: `web/src/components/character/AttributesTable.vue`

**Interfaces:**
- Produces: `ATTRIBUTES: readonly { name: string; abbr: string }[]` and `potCost(initial: number, target: number): number`, both exported from `@/lib/attributes` — consumed by Task 4's `AssignStatsBudgetModal.vue` and Task 5's `AttributesTable.vue` wiring.

- [ ] **Step 1: Create the shared module**

Create `web/src/lib/attributes.ts`:

```typescript
// Display metadata only (name + abbreviation) — fixed for every Character regardless of Ruleset,
// unlike the per-Character temp/pot/bonus values themselves. Shared by AttributesTable.vue (the
// read-only display) and AssignStatsBudgetModal.vue (the Pot-editing modal it opens), so the two
// can never drift out of sync on which ten attributes exist. Moved here from AttributesTable.vue,
// which used to declare this locally as its own ATTRIBUTES constant.
export const ATTRIBUTES = [
  { name: 'Strength', abbr: 'ST' },
  { name: 'Agility', abbr: 'AG' },
  { name: 'Constitution', abbr: 'CO' },
  { name: 'Quickness', abbr: 'QU' },
  { name: 'Self Discipline', abbr: 'SD' },
  { name: 'Memory', abbr: 'ME' },
  { name: 'Reasoning', abbr: 'RE' },
  { name: 'Empathy', abbr: 'EM' },
  { name: 'Presence', abbr: 'PR' },
  { name: 'Intuition', abbr: 'IN' },
] as const

// potCost computes the statBudget cost of raising a single attribute's Pot from initial to
// target, per the tiered rule from
// docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md Decision 1: the portion of the
// increase at or below Pot 90 costs 1 statBudget point per Pot point; the portion above Pot 90
// costs 5 statBudget points per Pot point. Mirrors potCost in
// internal/engine/timadorus/character_processor.go exactly — the engine is the authority; this
// copy only drives AssignStatsBudgetModal.vue's live "Budget remaining" feedback and blur-time
// validation, both of which the engine independently re-derives and re-checks itself.
export function potCost(initial: number, target: number): number {
  const below = Math.max(0, Math.min(target, 90) - initial)
  const above = Math.max(0, target - Math.max(initial, 90))
  return below + above * 5
}
```

- [ ] **Step 2: Refactor `AttributesTable.vue` to import `ATTRIBUTES` instead of declaring it locally**

In `web/src/components/character/AttributesTable.vue`, replace:

```typescript
<script setup lang="ts">
const props = defineProps<{
  attributes: Record<string, { temp: number; pot: number; bonus: number }>
}>()

// Display metadata only (name + abbreviation) — fixed for every Character regardless of Ruleset,
// unlike the per-Character temp/pot/bonus values themselves. Named ATTRIBUTES (not `attributes`)
// to avoid shadowing the `attributes` prop above.
const ATTRIBUTES = [
  { name: 'Strength', abbr: 'ST' },
  { name: 'Agility', abbr: 'AG' },
  { name: 'Constitution', abbr: 'CO' },
  { name: 'Quickness', abbr: 'QU' },
  { name: 'Self Discipline', abbr: 'SD' },
  { name: 'Memory', abbr: 'ME' },
  { name: 'Reasoning', abbr: 'RE' },
  { name: 'Empathy', abbr: 'EM' },
  { name: 'Presence', abbr: 'PR' },
  { name: 'Intuition', abbr: 'IN' },
]
```

with:

```typescript
<script setup lang="ts">
import { ATTRIBUTES } from '@/lib/attributes'

const props = defineProps<{
  attributes: Record<string, { temp: number; pot: number; bonus: number }>
}>()
```

(Named `ATTRIBUTES`, not `attributes`, still avoids shadowing the `attributes` prop — same reasoning as the removed comment, now living in `attributes.ts` itself.)

- [ ] **Step 3: Typecheck**

Run: `cd web && npm run typecheck`
Expected: no errors.

- [ ] **Step 4: Run the existing Attributes e2e spec to confirm no regression**

Run: `cd web && npx playwright test e2e/character-attributes.spec.ts --reporter=line`
Expected: all tests still PASS (this refactor changes no behavior, only where `ATTRIBUTES` is declared).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/attributes.ts web/src/components/character/AttributesTable.vue
git commit -m "refactor(web): extract shared ATTRIBUTES metadata and potCost into lib/attributes.ts

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 4: Frontend — `AssignStatsBudgetModal.vue`

**Files:**
- Create: `web/src/components/character/AssignStatsBudgetModal.vue`

**Interfaces:**
- Consumes: `ATTRIBUTES`, `potCost` (Task 3); `BaseModal.vue` (`title` prop, `close` emit), `BaseButton.vue` (`variant`, `disabled` props), `ErrorBanner.vue` (`message` prop, `dismiss` emit) — all pre-existing in `web/src/components/common/`.
- Produces: a component with props `{ attributes: Record<string, { temp: number; pot: number; bonus: number }>; statBudget: number; status: 'idle' | 'pending' | 'error'; errorMessage: string | null }` and emits `cancel: []`, `submit: [pot: Record<string, number>]`, `dismissError: []` — consumed by Task 5's `AttributesTable.vue`.
- Test IDs produced (used by Task 6's e2e spec): `assign-pot-${abbr}` on each Pot `<input>`, `budget-remaining` on the remaining-budget readout.

- [ ] **Step 1: Write the component**

Create `web/src/components/character/AssignStatsBudgetModal.vue`:

```vue
<script setup lang="ts">
import { computed, ref } from 'vue'
import BaseModal from '@/components/common/BaseModal.vue'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import { ATTRIBUTES, potCost } from '@/lib/attributes'

const props = defineProps<{
  attributes: Record<string, { temp: number; pot: number; bonus: number }>
  statBudget: number
  status: 'idle' | 'pending' | 'error'
  errorMessage: string | null
}>()
const emit = defineEmits<{
  cancel: []
  submit: [pot: Record<string, number>]
  dismissError: []
}>()

// Snapshot taken once, when this component is created — AttributesTable.vue's own `v-if` means a
// fresh instance is created every time the modal opens, and this deliberately does NOT resync if
// `attributes` changes again while the modal stays open (a short-lived draft, like
// `nameDraft`/`traitToAdd` elsewhere in this codebase, not a persistent field needing the fuller
// resync ConfigurationPanel.vue's Max Stat Budget input needed). `initialPot[abbr]` is both the
// floor a target can't drop below and the baseline potCost measures increases from — see
// docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md Decision 2.
const initialPot: Record<string, number> = {}
for (const { abbr } of ATTRIBUTES) {
  initialPot[abbr] = props.attributes[abbr]?.pot ?? 0
}

// The current draft value per attribute, and the last value known valid — reverted to on an
// invalid blur (see onBlur), not always back to initialPot, so a run of valid edits followed by
// one invalid one only reverts the invalid one.
const draft = ref<Record<string, number>>({ ...initialPot })
const lastValid = ref<Record<string, number>>({ ...initialPot })

const totalSpent = computed(() =>
  ATTRIBUTES.reduce((sum, { abbr }) => sum + potCost(initialPot[abbr], draft.value[abbr]), 0),
)
const budgetRemaining = computed(() => props.statBudget - totalSpent.value)

// Runs when a Pot input loses focus. By this point v-model.number has already written the
// just-typed value into draft.value[abbr], so totalSpent (above) already reflects it — a budget
// violation is exactly totalSpent exceeding statBudget, no separate "cost of just this field"
// computation needed.
function onBlur(abbr: string) {
  const value = draft.value[abbr]
  const floor = initialPot[abbr]
  const valid = Number.isInteger(value) && value >= floor && value <= 100 && totalSpent.value <= props.statBudget
  if (!valid) {
    draft.value[abbr] = lastValid.value[abbr]
  } else {
    lastValid.value[abbr] = value
  }
}

function onSubmit() {
  emit('submit', { ...draft.value })
}
</script>

<template>
  <BaseModal title="Assign Stats Budget" @close="emit('cancel')">
    <table class="mb-4 w-full text-sm">
      <thead>
        <tr class="border-b border-slate-100 text-left text-xs font-medium text-slate-500">
          <th class="pb-1.5">Attribute</th>
          <th class="pb-1.5">Abbr</th>
          <th class="pb-1.5">Pot</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="a in ATTRIBUTES" :key="a.abbr" class="border-b border-slate-50 last:border-0">
          <td class="py-1.5 text-slate-900">{{ a.name }}</td>
          <td class="py-1.5 text-slate-500">{{ a.abbr }}</td>
          <td class="py-1.5">
            <input
              v-model.number="draft[a.abbr]"
              type="number"
              step="1"
              class="w-20 rounded-md border border-slate-300 px-2 py-1 text-sm"
              :disabled="status === 'pending'"
              :data-testid="`assign-pot-${a.abbr}`"
              @blur="onBlur(a.abbr)"
            />
          </td>
        </tr>
      </tbody>
    </table>

    <div class="mb-4 rounded-md border border-slate-100 bg-slate-50 p-3 text-xs text-slate-600">
      <p class="mb-1 font-medium text-slate-900" data-testid="budget-remaining">Budget remaining: {{ budgetRemaining }}</p>
      <p>Set potential values. Pot &le; 90 equals 1 budget point per attribute point. 91-100 cost 5 budget points per attribute point.</p>
    </div>

    <span v-if="status === 'pending'" class="mb-3 block text-xs text-slate-400">Update requested — refreshing…</span>
    <ErrorBanner v-if="status === 'error'" :message="errorMessage" @dismiss="emit('dismissError')" />

    <div class="flex justify-end gap-2">
      <BaseButton variant="secondary" :disabled="status === 'pending'" @click="emit('cancel')">Cancel</BaseButton>
      <BaseButton :disabled="status === 'pending'" @click="onSubmit">Submit</BaseButton>
    </div>
  </BaseModal>
</template>
```

- [ ] **Step 2: Typecheck**

Run: `cd web && npm run typecheck`
Expected: no errors. (This component has no consumer yet — Task 5 wires it up — so this step is the only verification available until then.)

- [ ] **Step 3: Commit**

```bash
git add web/src/components/character/AssignStatsBudgetModal.vue
git commit -m "feat(web): add AssignStatsBudgetModal presentational component

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 5: Frontend — wire the modal into `AttributesTable.vue` and `CharacterDetailView.vue`

**Files:**
- Modify: `web/src/components/character/AttributesTable.vue`
- Modify: `web/src/views/CharacterDetailView.vue`

**Interfaces:**
- Consumes: `AssignStatsBudgetModal.vue` (Task 4); `useCharacters().requestAction(id: string, payload: Record<string, unknown>): Promise<void>` (pre-existing, `web/src/composables/useCharacters.ts`).
- Produces: `AttributesTable.vue` now takes two additional required props, `characterId: string` and `statBudget: number` — the "Assign Stats Budget" button (`data-testid` inherited from its own `attributes-card` container) shows only when `statBudget > 0`.

- [ ] **Step 1: Add `statBudget` to `CharacterDetailView.vue` and pass the new props down**

In `web/src/views/CharacterDetailView.vue`, add this computed directly after the existing `attributes` computed (both parse `character.value?.info` the same way):

```typescript
const statBudget = computed<number>(() => {
  try {
    return JSON.parse(character.value?.info || '{}')?.stats?.statBudget ?? 0
  } catch {
    return 0
  }
})
```

Then update the `<AttributesTable>` tag:

```html
      <AttributesTable class="flex-1" :attributes="attributes" :character-id="character.id" :stat-budget="statBudget" />
```

- [ ] **Step 2: Typecheck (expect a new failure)**

Run: `cd web && npm run typecheck`
Expected: FAIL — `AttributesTable.vue` doesn't yet declare `characterId`/`statBudget` props, so passing them is a type error. (Confirms the wiring is actually required, not just cosmetic.)

- [ ] **Step 3: Wire the button, modal, and submit/pending/confirm logic into `AttributesTable.vue`**

Replace the full `<script setup>` block of `web/src/components/character/AttributesTable.vue` with:

```vue
<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { ATTRIBUTES } from '@/lib/attributes'
import { useCharacters } from '@/composables/useCharacters'
import AssignStatsBudgetModal from './AssignStatsBudgetModal.vue'

const props = defineProps<{
  attributes: Record<string, { temp: number; pot: number; bonus: number }>
  characterId: string
  statBudget: number
}>()
const { requestAction } = useCharacters()

// '—' distinguishes "not yet seeded" (non-Timadorus Character, or the engine hasn't caught up
// right after creation — the same async-settling window traits/traitPoints already tolerate)
// from a genuinely-zero value.
function cell(abbr: string, field: 'temp' | 'pot'): string {
  const value = props.attributes[abbr]?.[field]
  return value === undefined ? '—' : String(value)
}

function bonusCell(abbr: string): string {
  const value = props.attributes[abbr]?.bonus
  if (value === undefined) return '—'
  return value >= 0 ? `+${value}` : String(value)
}

const showAssignModal = ref(false)
function openAssignModal() {
  // Reset any stale error/pending state from a previous attempt before showing a fresh modal —
  // otherwise reopening after a timed-out submit would show its old error banner immediately.
  submitPotStatus.value = 'idle'
  submitPotError.value = null
  showAssignModal.value = true
}

type SubmitPotStatus = 'idle' | 'pending' | 'error'
const submitPotStatus = ref<SubmitPotStatus>('idle')
const submitPotError = ref<string | null>(null)
// The batch most recently submitted, so the watch below can tell "the loaded attributes now
// reflect my own request" apart from "someone else changed something unrelated".
let pendingPot: Record<string, number> | null = null

// A rejected submitPot (insufficient budget, an out-of-range target, an unknown abbreviation, or
// an archived Character) is a clean, logged engine-side no-op — no event distinguishes "rejected"
// from "still processing," so this timeout guarantees the modal always resolves, exactly like
// BaseInfoTable.vue's ADD_TRAIT_TIMEOUT_MS for Add Trait.
const SUBMIT_POT_TIMEOUT_MS = 10000
let submitPotTimeoutHandle: ReturnType<typeof setTimeout> | null = null
function clearSubmitPotTimeout() {
  if (submitPotTimeoutHandle) {
    clearTimeout(submitPotTimeoutHandle)
    submitPotTimeoutHandle = null
  }
}

async function onSubmitPot(pot: Record<string, number>) {
  submitPotStatus.value = 'pending'
  submitPotError.value = null
  pendingPot = pot
  clearSubmitPotTimeout()
  submitPotTimeoutHandle = setTimeout(() => {
    if (submitPotStatus.value === 'pending') {
      submitPotStatus.value = 'error'
      submitPotError.value = 'No confirmation received — this change may not have been applied.'
      pendingPot = null
    }
  }, SUBMIT_POT_TIMEOUT_MS)
  try {
    await requestAction(props.characterId, { action: 'submitPot', pot })
  } catch (err) {
    clearSubmitPotTimeout()
    submitPotStatus.value = 'error'
    submitPotError.value = err instanceof Error ? err.message : 'Failed to request the Pot change.'
  }
}

// Closes the modal only once the loaded attributes actually reflect every submitted target — a
// batch submitPot rejected by the engine leaves attributes unchanged, so this deliberately never
// fires for a rejection (the timeout above handles that case instead).
watch(
  () => props.attributes,
  (attributes) => {
    if (
      submitPotStatus.value === 'pending' &&
      pendingPot !== null &&
      Object.entries(pendingPot).every(([abbr, target]) => attributes[abbr]?.pot === target)
    ) {
      clearSubmitPotTimeout()
      submitPotStatus.value = 'idle'
      pendingPot = null
      showAssignModal.value = false
    }
  },
)

onUnmounted(clearSubmitPotTimeout)
</script>
```

Then update the `<template>` block: replace

```html
    <h2 class="mb-3 text-sm font-semibold text-slate-900">Attributes</h2>
```

with:

```html
    <div class="mb-3 flex items-center justify-between">
      <h2 class="text-sm font-semibold text-slate-900">Attributes</h2>
      <button v-if="statBudget > 0" class="text-xs text-indigo-600 hover:underline" @click="openAssignModal">
        Assign Stats Budget
      </button>
    </div>
```

And add the modal at the end of the root `<div>`, directly after the closing `</table>`:

```html
    <AssignStatsBudgetModal
      v-if="showAssignModal"
      :attributes="attributes"
      :stat-budget="statBudget"
      :status="submitPotStatus"
      :error-message="submitPotError"
      @cancel="showAssignModal = false"
      @submit="onSubmitPot"
      @dismiss-error="submitPotStatus = 'idle'"
    />
```

- [ ] **Step 4: Typecheck**

Run: `cd web && npm run typecheck`
Expected: no errors.

- [ ] **Step 5: Run the existing Attributes and Traits e2e specs to confirm no regression**

Run: `cd web && npx playwright test e2e/character-attributes.spec.ts e2e/character-traits.spec.ts e2e/character-configuration.spec.ts --reporter=line`
Expected: all tests still PASS. (Neither existing spec seeds `stats.statBudget`, so `statBudget` computes to `0` and the new button stays hidden — no visible change to these flows.)

- [ ] **Step 6: Commit**

```bash
git add web/src/components/character/AttributesTable.vue web/src/views/CharacterDetailView.vue
git commit -m "feat(web): wire Assign Stats Budget button and submit/confirm flow into AttributesTable

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 6: Frontend e2e — `character-stats-budget.spec.ts`

**Files:**
- Create: `web/e2e/character-stats-budget.spec.ts`

**Interfaces:**
- Consumes: `createMockState`/`installMockBackend` (`web/e2e/support/mockBackend.ts`), `seedAuth` (`web/e2e/support/auth.ts`) — both pre-existing, used unchanged (the mock backend's generic `PUT /api/command/characters/:characterId/action` passthrough needs no changes for a new action name).

- [ ] **Step 1: Write the spec file**

Create `web/e2e/character-stats-budget.spec.ts`:

```typescript
import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'
const ABBRS = ['ST', 'AG', 'CO', 'QU', 'SD', 'ME', 'RE', 'EM', 'PR', 'IN']

function defaultAttributes(overrides: Partial<Record<string, number>> = {}) {
  const attrs: Record<string, { temp: number; pot: number; bonus: number }> = {}
  for (const abbr of ABBRS) {
    attrs[abbr] = { temp: 50, pot: overrides[abbr] ?? 50, bonus: 0 }
  }
  return attrs
}

function seedState(
  opts: { statBudget?: number; attrs?: Partial<Record<string, number>> } = {},
  overrides: Partial<MockState> = {},
): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    campaigns: [
      {
        id: 'c1',
        universeId: 'u1',
        name: 'Test Campaign',
        rulesetId: 'r1',
        isArchived: false,
        configuration: JSON.stringify({ characterCreation: { maxStatBudget: 35 } }),
      },
    ],
    rulesets: [{ id: 'r1', name: 'Timadorus' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    gamemasterIds: ['user-1'],
    characters: [
      {
        id: 'ch1',
        name: 'Aragorn',
        campaignId: 'c1',
        entityId: 'e1',
        playerUserId: 'user-1',
        isArchived: false,
        info: JSON.stringify({
          stats: { traitPoints: 2, traits: [], attributes: defaultAttributes(opts.attrs), statBudget: opts.statBudget ?? 40 },
        }),
      },
    ],
    entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
    ...overrides,
  })
}

test('the Assign Stats Budget button only shows when statBudget is greater than 0', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 0 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')

  const attributesCard = page.getByTestId('attributes-card')
  await expect(attributesCard).toBeVisible()
  await expect(attributesCard.getByRole('button', { name: 'Assign Stats Budget' })).not.toBeVisible()
})

test('the button opens a modal listing all 10 attributes’ Pot values and the explanation text', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 40 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()

  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  for (const abbr of ABBRS) {
    await expect(dialog.getByTestId(`assign-pot-${abbr}`)).toHaveValue('50')
  }
  await expect(
    dialog.getByText('Set potential values. Pot ≤ 90 equals 1 budget point per attribute point. 91-100 cost 5 budget points per attribute point.'),
  ).toBeVisible()
})

test('an invalid edit reverts to its last valid value on blur, and Budget remaining updates only for valid edits', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 20 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
  const dialog = page.getByRole('dialog')
  const stInput = dialog.getByTestId('assign-pot-ST')
  const remaining = dialog.getByTestId('budget-remaining')
  await expect(remaining).toHaveText('Budget remaining: 20')

  // Below the floor (50) — reverts.
  await stInput.fill('40')
  await stInput.blur()
  await expect(stInput).toHaveValue('50')
  await expect(remaining).toHaveText('Budget remaining: 20')

  // Above 100 — reverts.
  await stInput.fill('101')
  await stInput.blur()
  await expect(stInput).toHaveValue('50')

  // A valid increase (50 -> 60) costs 10, leaving 10.
  await stInput.fill('60')
  await stInput.blur()
  await expect(stInput).toHaveValue('60')
  await expect(remaining).toHaveText('Budget remaining: 10')

  // 60 -> 71 would cost 11, exceeding the 10 remaining — reverts to the last VALID value (60),
  // not all the way back to the original floor (50).
  await stInput.fill('71')
  await stInput.blur()
  await expect(stInput).toHaveValue('60')
  await expect(remaining).toHaveText('Budget remaining: 10')
})

test('Cancel closes the modal without sending a request', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 40 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  const apiCalls = await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByTestId('assign-pot-ST').fill('60')
  await dialog.getByTestId('assign-pot-ST').blur()

  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(dialog).not.toBeVisible()
  expect(apiCalls).not.toContain('PUT /api/command/characters/ch1/action')
})

test('Submit sends the submitPot payload with all 10 abbreviations, shows pending state, and closes once confirmed', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 40 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  const apiCalls = await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByTestId('assign-pot-ST').fill('60')
  await dialog.getByTestId('assign-pot-ST').blur()

  const submitRequest = page.waitForRequest(
    (req) => req.method() === 'PUT' && req.url().endsWith('/api/command/characters/ch1/action'),
  )
  await dialog.getByRole('button', { name: 'Submit' }).click()

  // Asserting only "fired" would still pass with a wrong JSON shape (e.g. a flattened payload
  // instead of the nested `pot` object) — the engine fails closed on an unrecognized payload, so
  // every real Submit would silently no-op in production while this whole suite stayed green.
  const request = await submitRequest
  expect(request.postDataJSON()).toEqual({
    action: 'submitPot',
    pot: { ST: 60, AG: 50, CO: 50, QU: 50, SD: 50, ME: 50, RE: 50, EM: 50, PR: 50, IN: 50 },
  })
  await expect(async () => {
    expect(apiCalls).toContain('PUT /api/command/characters/ch1/action')
  }).toPass()

  await expect(dialog.getByText('Update requested — refreshing…')).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Submit' })).toBeDisabled()
  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeDisabled()

  // Simulate the async engine mutation + change-feed catching up, exactly like
  // character-traits.spec.ts does for the sibling Add Trait flow.
  const character = state.characters.find((c) => c.id === 'ch1')!
  const info = JSON.parse(character.info)
  info.stats.attributes.ST.pot = 60
  info.stats.statBudget = 30
  character.info = JSON.stringify(info)
  state.changes.push({
    globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
    universeId: 'u1',
    aggregateType: 'character',
    aggregateId: 'ch1',
    eventType: 'character.action_applied.v1',
    occurredAt: new Date().toISOString(),
  })

  await expect(dialog).not.toBeVisible({ timeout: 10000 })
  await expect(page.getByTestId('attribute-ST-pot')).toHaveText('60')
})

test('a submitPot request that never gets confirmed times out with an error and re-enables Cancel/Submit', async ({
  page,
  context,
  baseURL,
}) => {
  // The pending-timeout itself is real (SUBMIT_POT_TIMEOUT_MS = 10s in AttributesTable.vue) —
  // mirrors character-traits.spec.ts's own identical pattern for Add Trait.
  test.setTimeout(30_000)
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 40 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByTestId('assign-pot-ST').fill('60')
  await dialog.getByTestId('assign-pot-ST').blur()
  await dialog.getByRole('button', { name: 'Submit' }).click()
  await expect(dialog.getByText('Update requested — refreshing…')).toBeVisible()

  // Never push a confirming change — simulates a rejected submitPot (insufficient budget, an
  // out-of-range target, or an archived Character), all of which the engine logs and silently
  // no-ops on, never emitting a change that would let the attributes catch up.
  await expect(dialog.getByText('No confirmation received', { exact: false })).toBeVisible({ timeout: 15_000 })
  await expect(dialog.getByText('Update requested — refreshing…')).not.toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Submit' })).toBeEnabled()
  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeEnabled()
})
```

- [ ] **Step 2: Run the new spec**

Run: `cd web && npx playwright test e2e/character-stats-budget.spec.ts --reporter=line`
Expected: all 6 tests PASS.

- [ ] **Step 3: Run the full e2e suite to confirm no regressions anywhere else**

Run: `cd web && npm run test:e2e -- --reporter=line`
Expected: all tests PASS (pre-existing suite plus the new file).

- [ ] **Step 4: Final typecheck**

Run: `cd web && npm run typecheck`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add web/e2e/character-stats-budget.spec.ts
git commit -m "test(web): add e2e coverage for the Assign Stats Budget modal

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```
