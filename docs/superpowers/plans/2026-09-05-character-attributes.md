# Character Attributes (Temp/Pot/Bonus) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `AttributesTable.vue`'s fully static placeholder (every attribute always reading
`75`/`+0`) with real, engine-managed values stored in the Character's `info` field: a Temp column
(replacing the old "Value" column), a new Pot column, and Bonus (moved to the last column) —
all seeded by `timadorus-engine` on Character creation.

**Architecture:** Extends the existing `handleCharacterCreated` seeding this session's Character
Traits work already added — one more key (`attributes`) alongside `traitPoints`/`traits` in the
same `stats` object, written in the same `mutateInfo` call. `statBudget` is derived by the engine
itself from the Campaign's own `configuration` (never transmitted by the SPA) via a new shared
`loadCampaignConfiguration` helper, refactored out of the existing `loadCampaignTraits`. No new
event type, no new command endpoint, no OpenAPI change at all — the create-character command and
its schema are completely untouched by this work.

**Tech Stack:** Go, `pgx`, Vue 3 `<script setup>`, Playwright, Ginkgo/Gomega.

## Global Constraints

- Scope: "Timadorus" Ruleset only (case-insensitive), matching every other `timadorus-engine`
  default-seeding precedent in this codebase (traits, max stat budget).
- No changes to `api/command/openapi.yaml`, `CreateCharacterRequest`, `events.CharacterCreated`,
  `character.New(...)`, or any SPA character-creation call site. The SPA never transmits a
  `statBudget` value — the engine derives it itself from the Campaign's own `configuration`.
- All of `attributes` and `statBudget` are seeded in the **same** `mutateInfo` write
  `handleCharacterCreated` already uses for `traitPoints`/`traits` — no separate write, no
  clobbering risk between features.
- `attributes` is an object keyed by attribute **abbreviation** (`"ST"`, `"AG"`, ...), each value
  `{ "temp": number, "pot": number, "bonus": number }`. The ten abbreviations are hardcoded in the
  engine: `ST, AG, CO, QU, SD, ME, RE, EM, PR, IN`.
- Both Temp and Pot seed to `50`. `bonus = floor((temp - 50) / 10)` — always `0` at creation today,
  but written as a general function, not hardcoded to `0`.
- `statBudget` is read from the Campaign's `characterCreation.maxStatBudget` at the moment the
  Character is created; omitted from `stats` entirely if the Campaign has none configured (not an
  error).
- `go build ./... && go vet ./... && go test -count=1 ./...` must stay clean after every task;
  `npm run build` and the full Playwright suite must stay clean after every SPA-touching task.

---

### Task 1: `timadorus-engine` — seed `attributes` and `statBudget` on Character creation

**Files:**
- Modify: `internal/engine/timadorus/character_processor.go`
- Modify: `internal/engine/timadorus/character_processor_test.go`
- Create: `internal/engine/timadorus/attribute_bonus_internal_test.go`

**Interfaces:**
- Produces: `defaultAttributes() map[string]any` (package-private) — builds the seed value for
  `stats.attributes`.
- Produces: `attributeBonus(temp int) int` (package-private) — the placeholder bonus formula.
- Produces: `loadCampaignConfiguration(ctx, tx, campaignID) (campaignConfiguration, error)`
  (package-private) — replaces `loadCampaignTraits`'s own inline query; `loadCampaignTraits` becomes
  a thin wrapper over it.

- [ ] **Step 1: Refactor the campaign-configuration read and add the attribute helpers**

Replace `internal/engine/timadorus/character_processor.go`'s imports (add `"math"`):

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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/character"
	"github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
	"github.com/timadorus/platform/internal/observability"
)
```

In `CharacterProcessor`'s own type doc comment, replace the first paragraph of the "Reacts to two
event types..." block:

```go
// Reacts to two event types on a "timadorus"-ruleset Character's Campaign: CharacterCreated seeds
// stats.traitPoints/stats.traits with their starting defaults, and ActionRequested recognizes a
// {"action":"addTrait","trait":"<name>"} payload — validating it against the Campaign's own
// configured trait list, the Character's current traitPoints, and its existing traits before
// applying it, logging (and no-op'ing) any rejection instead of erroring the event. Any other
// ActionRequested payload falls back to appending occurredAt to info's "actions" array, exactly
// as before.
```

with:

```go
// Reacts to two event types on a "timadorus"-ruleset Character's Campaign: CharacterCreated seeds
// stats.traitPoints/stats.traits/stats.attributes with their starting defaults, plus
// stats.statBudget read from the Campaign's own configuration (see handleCharacterCreated's doc
// comment), and ActionRequested recognizes a {"action":"addTrait","trait":"<name>"} payload —
// validating it against the Campaign's own configured trait list, the Character's current
// traitPoints, and its existing traits before applying it, logging (and no-op'ing) any rejection
// instead of erroring the event. Any other ActionRequested payload falls back to appending
// occurredAt to info's "actions" array, exactly as before.
```

Replace the `defaultTraitPoints` var block with (adds the attribute-seeding constants alongside
it):

```go
// defaultTraitPoints is the number of trait points a new "timadorus"-ruleset Character starts
// with — CharacterCreated's own default-seeding sibling to CampaignProcessor's
// defaultTraits/defaultMaxStatBudget. Not a `const` for the same reason those aren't: matches
// this package's established shape for a default seed value, even though a plain int could be a
// const on its own.
var defaultTraitPoints = 2

// attributeAbbreviations is timadorus-engine's own hardcoded canonical list of the ten
// character-sheet attributes a Character starts with — mirrors defaultTraits' shape as a
// package-level default seed list, not something read from anywhere in the Campaign's
// configuration.
var attributeAbbreviations = []string{"ST", "AG", "CO", "QU", "SD", "ME", "RE", "EM", "PR", "IN"}

// initialAttributeValue is both Temp and Pot's starting value for every attribute of a newly
// created "timadorus"-ruleset Character.
var initialAttributeValue = 50

// attributeBonus computes an attribute's Bonus from its current Temp value. This is a
// placeholder formula (floor((temp-50)/10), zero at the baseline Temp of 50) standing in for the
// real timadorus-engine rules formula, which is not yet specified — see design spec "Character
// Attributes (Temp/Pot/Bonus)", Decision 4. Written generally (not hardcoded to 0) so it stays
// correct once something other than character creation can change Temp.
func attributeBonus(temp int) int {
	return int(math.Floor(float64(temp-50) / 10))
}

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

Replace `handleCharacterCreated`'s doc comment and body with:

```go
// handleCharacterCreated merges the default stats object into a newly created Character's info,
// but only if that Character's Campaign uses the "timadorus" Ruleset. Resolves the ruleset via
// the shared cache's own campaigns_read_model join (RulesetCache.resolve) — CharacterCreated
// carries CampaignID directly, unlike ActionRequested's envelope (which only carries the
// Character's own id and needs an extra characters_read_model hop to find it), but it does NOT
// carry RulesetID the way CampaignCreated does, so — unlike CampaignProcessor.handleCampaignCreated's
// event-store-based resolution — this cannot skip the read-model join on a cache miss.
//
// Also reads the Campaign's own configuration directly (loadCampaignConfiguration) to seed
// stats.statBudget from characterCreation.maxStatBudget — the engine derives this itself rather
// than trusting a client-submitted value (design spec "Character Attributes", Decision 3), the
// same "engine, not the SPA, is the source of truth" principle tryAddTrait already applies to
// trait eligibility. Omitted from stats entirely if the Campaign has no maxStatBudget configured
// (not expected for a "timadorus" Campaign, since CampaignProcessor's own handleCampaignCreated
// always seeds a default — but not treated as an error if it's ever missing).
func (p *CharacterProcessor) handleCharacterCreated(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.CharacterCreated
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf(errPrefix+"unmarshal %s: %w", env.EventType, err)
	}

	rulesetName, err := p.cache.resolve(ctx, tx, e.CampaignID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(rulesetName, targetRulesetName) {
		return nil // not our ruleset — no-op, still checkpointed as handled
	}

	config, err := loadCampaignConfiguration(ctx, tx, e.CampaignID)
	if err != nil {
		return err
	}

	return p.mutateInfo(ctx, tx, env, env.AggregateID, func(info map[string]any) {
		stats := map[string]any{
			"traitPoints": defaultTraitPoints,
			"traits":      []string{},
			"attributes":  defaultAttributes(),
		}
		if config.CharacterCreation.MaxStatBudget != nil {
			stats["statBudget"] = *config.CharacterCreation.MaxStatBudget
		}
		info["stats"] = stats
	})
}
```

Replace the existing `loadCampaignTraits` function (and its doc comment) with:

```go
// campaignConfiguration is the subset of a Campaign's own opaque configuration JSON this package
// reads: its own configured trait list (tryAddTrait's trait-eligibility check) and its
// character-creation stat budget (handleCharacterCreated's stats.statBudget seeding). Both are
// read from the same campaigns_read_model.configuration column via loadCampaignConfiguration —
// one query serving both call sites, matching RulesetCache.resolve's own already-established
// cross-projection read pattern.
type campaignConfiguration struct {
	Traits            []string `json:"traits"`
	CharacterCreation struct {
		MaxStatBudget *float64 `json:"maxStatBudget"`
	} `json:"characterCreation"`
}

// loadCampaignConfiguration reads and parses a Campaign's own configuration column directly from
// campaigns_read_model. Best-effort on parse failure or an absent/empty column: an unparseable or
// absent configuration yields a zero-value campaignConfiguration (not an error) — same "start
// fresh rather than error" philosophy mutateInfo/mutateConfiguration already use for a malformed
// opaque JSON field.
func loadCampaignConfiguration(ctx context.Context, tx pgx.Tx, campaignID uuid.UUID) (campaignConfiguration, error) {
	var raw string
	if err := tx.QueryRow(ctx,
		`SELECT configuration FROM campaigns_read_model WHERE id = $1`, campaignID,
	).Scan(&raw); err != nil {
		return campaignConfiguration{}, fmt.Errorf(errPrefix+"look up configuration for campaign %s: %w", campaignID, err)
	}
	var config campaignConfiguration
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &config) // best-effort; zero value on failure
	}
	return config, nil
}

// loadCampaignTraits extracts the Campaign's own configured trait list — see
// loadCampaignConfiguration for the underlying read this package shares with
// handleCharacterCreated's stats.statBudget seeding.
func loadCampaignTraits(ctx context.Context, tx pgx.Tx, campaignID uuid.UUID) ([]string, error) {
	config, err := loadCampaignConfiguration(ctx, tx, campaignID)
	if err != nil {
		return nil, err
	}
	return config.Traits, nil
}
```

Run: `go build ./... && go vet ./...` — expect clean.

- [ ] **Step 2: Add the internal (white-box) bonus-formula test**

`attributeBonus` is package-private and this package's existing tests
(`character_processor_test.go`, `shared_cache_test.go`, etc.) are all `package timadorus_test`
(black-box) — they can't call it directly. `cache_test.go` already establishes the precedent for a
same-package (`package timadorus`) internal test file for exactly this reason.

Create `internal/engine/timadorus/attribute_bonus_internal_test.go`:

```go
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
```

Run: `go test ./internal/engine/timadorus/... -run TestAttributeBonus -v` — expect PASS.

- [ ] **Step 3: Add the black-box CharacterCreated seeding tests**

Add to `internal/engine/timadorus/character_processor_test.go`, immediately after
`TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStats` (which stays exactly as-is —
extend its assertions in place, per below, rather than duplicating the whole test):

First, extend the **existing** `TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStats`
test (does not seed a `characterCreation.maxStatBudget`, so this exercises the "no statBudget
configured" path at the same time as attributes) — replace its decode struct and assertions with:

```go
	var decoded struct {
		Stats struct {
			TraitPoints int            `json:"traitPoints"`
			Traits      []string       `json:"traits"`
			Attributes  map[string]any `json:"attributes"`
			StatBudget  *float64       `json:"statBudget"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(infoEvent.Info), &decoded); err != nil {
		t.Fatalf("info %q is not the expected shape: %v", infoEvent.Info, err)
	}
	if decoded.Stats.TraitPoints != 2 {
		t.Fatalf("got traitPoints %d, want 2", decoded.Stats.TraitPoints)
	}
	if len(decoded.Stats.Traits) != 0 {
		t.Fatalf("got traits %v, want none", decoded.Stats.Traits)
	}
	wantAbbreviations := []string{"ST", "AG", "CO", "QU", "SD", "ME", "RE", "EM", "PR", "IN"}
	if len(decoded.Stats.Attributes) != len(wantAbbreviations) {
		t.Fatalf("got %d attributes, want %d: %v", len(decoded.Stats.Attributes), len(wantAbbreviations), decoded.Stats.Attributes)
	}
	for _, abbr := range wantAbbreviations {
		raw, ok := decoded.Stats.Attributes[abbr]
		if !ok {
			t.Fatalf("missing attribute %q", abbr)
		}
		attr, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("attribute %q is not an object: %v", abbr, raw)
		}
		if attr["temp"] != float64(50) {
			t.Fatalf("attribute %q temp = %v, want 50", abbr, attr["temp"])
		}
		if attr["pot"] != float64(50) {
			t.Fatalf("attribute %q pot = %v, want 50", abbr, attr["pot"])
		}
		if attr["bonus"] != float64(0) {
			t.Fatalf("attribute %q bonus = %v, want 0", abbr, attr["bonus"])
		}
	}
	if decoded.Stats.StatBudget != nil {
		t.Fatalf("got statBudget %v, want none (Campaign has no characterCreation configured)", *decoded.Stats.StatBudget)
	}
```

Then add a new test after it, seeding a `characterCreation.maxStatBudget` this time:

```go
func TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStatBudgetFromCampaign(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "Timadorus")
	seedCampaignConfiguration(t, pool, campaignID, `{"characterCreation":{"maxStatBudget":40}}`)

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	c, err := character.New(campaignID, uuid.New(), uuid.New(), "Elminster")
	if err != nil {
		t.Fatalf("character.New: %v", err)
	}
	characterID := c.AggregateID()
	if err := repo.Save(context.Background(), c); err != nil {
		t.Fatalf("save character: %v", err)
	}

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   characterID,
		AggregateType: events.AggregateType,
		Version:       1,
		EventType:     events.TypeCharacterCreated,
		Payload: mustMarshal(t, events.CharacterCreated{
			ID: characterID, Name: "Elminster", CampaignID: campaignID, EntityID: uuid.New(),
			PlayerUserID: uuid.New(), OccurredAt: time.Now().UTC(),
		}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2
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
			StatBudget *float64 `json:"statBudget"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(infoEvent.Info), &decoded); err != nil {
		t.Fatalf("info %q is not the expected shape: %v", infoEvent.Info, err)
	}
	if decoded.Stats.StatBudget == nil {
		t.Fatal("got no statBudget, want 40")
	}
	if *decoded.Stats.StatBudget != 40 {
		t.Fatalf("got statBudget %v, want 40", *decoded.Stats.StatBudget)
	}

	wait()
}
```

Run: `go test ./internal/engine/timadorus/... -run TestCharacterProcessor_CharacterCreated -v` —
expect all PASS.

- [ ] **Step 4: Run full regression check**

Run: `go build ./... && go vet ./... && go test -count=1 ./internal/engine/timadorus/...`
Expected: clean, all tests pass, including the pre-existing
`TestCharacterProcessor_CharacterCreated_NonMatchingRuleset_NoOp` (unchanged — its "no InfoChanged
event at all" assertion already covers attributes/statBudget too, since nothing is written at all
for a non-matching ruleset) and every `AddTrait`/timestamp-append test (unaffected by this task —
`loadCampaignTraits`'s public behavior is unchanged, only its internals were refactored).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/timadorus/character_processor.go \
        internal/engine/timadorus/character_processor_test.go \
        internal/engine/timadorus/attribute_bonus_internal_test.go
git commit -m "engine: seed Character attributes (Temp/Pot/Bonus) and statBudget on creation"
```

---

### Task 2: SPA — real `AttributesTable.vue` and Playwright coverage

**Files:**
- Modify: `web/src/components/character/AttributesTable.vue`
- Modify: `web/src/views/CharacterDetailView.vue`
- Create: `web/e2e/character-attributes.spec.ts`

**Interfaces:**
- Consumes: `info.stats.attributes` shape from Task 1 —
  `Record<string, { temp: number; pot: number; bonus: number }>` keyed by abbreviation.
- Produces: `AttributesTable`'s new required prop
  `attributes: Record<string, { temp: number; pot: number; bonus: number }>`.

- [ ] **Step 1: Rewrite `AttributesTable.vue` to be props-driven**

Replace `web/src/components/character/AttributesTable.vue` in full:

```vue
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
</script>

<template>
  <div class="rounded-md border border-slate-200 p-4" data-testid="attributes-card">
    <h2 class="mb-3 text-sm font-semibold text-slate-900">Attributes</h2>
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b border-slate-100 text-left text-xs font-medium text-slate-500">
          <th class="pb-1.5">Attribute</th>
          <th class="pb-1.5">Abbr</th>
          <th class="pb-1.5">Temp</th>
          <th class="pb-1.5">Pot</th>
          <th class="pb-1.5">Bonus</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="a in ATTRIBUTES" :key="a.abbr" class="border-b border-slate-50 last:border-0">
          <td class="py-1.5 text-slate-900">{{ a.name }}</td>
          <td class="py-1.5 text-slate-500">{{ a.abbr }}</td>
          <td class="py-1.5 text-slate-900" :data-testid="`attribute-${a.abbr}-temp`">{{ cell(a.abbr, 'temp') }}</td>
          <td class="py-1.5 text-slate-900" :data-testid="`attribute-${a.abbr}-pot`">{{ cell(a.abbr, 'pot') }}</td>
          <td class="py-1.5 text-slate-900" :data-testid="`attribute-${a.abbr}-bonus`">{{ bonusCell(a.abbr) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
```

- [ ] **Step 2: Wire real `attributes` data through `CharacterDetailView.vue`**

In `web/src/views/CharacterDetailView.vue`, immediately after the existing `availableTraits`
computed (the `traits`/`traitPoints`/`availableTraits` block), add:

```ts
const attributes = computed<Record<string, { temp: number; pot: number; bonus: number }>>(() => {
  try {
    return JSON.parse(character.value?.info || '{}')?.stats?.attributes ?? {}
  } catch {
    return {}
  }
})
```

In the template, change:

```html
      <AttributesTable class="flex-1" />
```

to:

```html
      <AttributesTable class="flex-1" :attributes="attributes" />
```

Run: `cd web && npm run build` — expect clean (type-checks the new prop).

- [ ] **Step 3: Playwright coverage**

Create `web/e2e/character-attributes.spec.ts`, mirroring `character-traits.spec.ts`'s seed/locator
conventions:

```ts
import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'

function seedState(overrides: Partial<MockState> = {}): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    campaigns: [
      {
        id: 'c1',
        universeId: 'u1',
        name: 'Test Campaign',
        rulesetId: 'r1',
        isArchived: false,
        configuration: JSON.stringify({ traits: ['strong', 'agile', 'quick'] }),
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
          stats: {
            attributes: {
              ST: { temp: 50, pot: 50, bonus: 0 },
              AG: { temp: 50, pot: 50, bonus: 0 },
              CO: { temp: 60, pot: 65, bonus: 1 },
              QU: { temp: 40, pot: 45, bonus: -1 },
              SD: { temp: 50, pot: 50, bonus: 0 },
              ME: { temp: 50, pot: 50, bonus: 0 },
              RE: { temp: 50, pot: 50, bonus: 0 },
              EM: { temp: 50, pot: 50, bonus: 0 },
              PR: { temp: 50, pot: 50, bonus: 0 },
              IN: { temp: 50, pot: 50, bonus: 0 },
            },
          },
        }),
      },
    ],
    entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
    ...overrides,
  })
}

test('the Attributes table shows the Character\'s real seeded Temp/Pot/Bonus values', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')

  const attributesCard = page.getByTestId('attributes-card')
  await expect(attributesCard).toBeVisible()

  await expect(attributesCard.getByTestId('attribute-ST-temp')).toHaveText('50')
  await expect(attributesCard.getByTestId('attribute-ST-pot')).toHaveText('50')
  await expect(attributesCard.getByTestId('attribute-ST-bonus')).toHaveText('+0')

  // A positive bonus.
  await expect(attributesCard.getByTestId('attribute-CO-temp')).toHaveText('60')
  await expect(attributesCard.getByTestId('attribute-CO-pot')).toHaveText('65')
  await expect(attributesCard.getByTestId('attribute-CO-bonus')).toHaveText('+1')

  // A negative bonus — must not gain a stray leading "+".
  await expect(attributesCard.getByTestId('attribute-QU-temp')).toHaveText('40')
  await expect(attributesCard.getByTestId('attribute-QU-pot')).toHaveText('45')
  await expect(attributesCard.getByTestId('attribute-QU-bonus')).toHaveText('-1')
})

test('a Character with no seeded attributes shows placeholders in every Temp/Pot/Bonus cell', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({
    characters: [
      {
        id: 'ch1',
        name: 'Aragorn',
        campaignId: 'c1',
        entityId: 'e1',
        playerUserId: 'user-1',
        isArchived: false,
        info: JSON.stringify({ stats: {} }),
      },
    ],
  })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')

  const attributesCard = page.getByTestId('attributes-card')
  for (const abbr of ['ST', 'AG', 'CO', 'QU', 'SD', 'ME', 'RE', 'EM', 'PR', 'IN']) {
    await expect(attributesCard.getByTestId(`attribute-${abbr}-temp`)).toHaveText('—')
    await expect(attributesCard.getByTestId(`attribute-${abbr}-pot`)).toHaveText('—')
    await expect(attributesCard.getByTestId(`attribute-${abbr}-bonus`)).toHaveText('—')
  }
})
```

- [ ] **Step 4: Run**

Run, from `web/` (using this sandbox's `LD_LIBRARY_PATH` workaround for the missing `libnspr4.so`
if applicable to your environment):
```bash
npx playwright test --reporter=list
```
Expected: every test passes, including the two new ones in `character-attributes.spec.ts` and
every pre-existing test (`AttributesTable`'s prop is now required, so any spec that renders the
Character detail page must still work — check for a rendering error if any pre-existing test now
fails, since Vue will warn but not throw on a missing required prop, so a real regression here
would most likely show up as an unrelated-looking assertion failure rather than a crash).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/character/AttributesTable.vue \
        web/src/views/CharacterDetailView.vue \
        web/e2e/character-attributes.spec.ts
git commit -m "web: replace the static Attributes table with real Temp/Pot/Bonus values"
```

---

### Task 3: Real end-to-end coverage (Go, real cluster)

**Files:**
- Modify: `test/e2e/e2e_test.go`

**Depends on:** Task 1 (asserts the real engine's seeding behavior against a live cluster).

- [ ] **Step 1: Extend the existing traits `It`'s default-stats assertion**

`test/e2e/e2e_test.go` already has an `It` (added by the Character Traits work) that creates a
Timadorus Campaign + Character and, before testing `addTrait`, waits for the engine to seed default
stats via an `Eventually` block. Extend that same block (don't create a second Universe/Campaign/
Character chain — this reuses the one already being created) to also assert `attributes` and
`statBudget`, and rename the `It` to reflect its now-broader scope.

Change the `It`'s title from `"adding a trait to a Character validates against its Campaign's own
trait list and eventually lands"` to:
```go
	It("creating a Character seeds its default stats (traitPoints, attributes, statBudget), and adding a trait validates against its Campaign's own trait list and eventually lands", func() {
```

Replace the first `Eventually` block (the one asserting `traitPoints`/`traits` right after Character
creation) with:

```go
		// The default stats object should already be present once the engine's CharacterCreated
		// handling catches up.
		Eventually(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, characterResp.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var info map[string]any
			g.Expect(json.Unmarshal([]byte(got.Info), &info)).To(Succeed())
			stats, _ := info["stats"].(map[string]any)
			g.Expect(stats["traitPoints"]).To(Equal(float64(2)))
			g.Expect(stats["traits"]).To(BeEmpty())

			attributes, _ := stats["attributes"].(map[string]any)
			g.Expect(attributes).To(HaveLen(10))
			for _, abbr := range []string{"ST", "AG", "CO", "QU", "SD", "ME", "RE", "EM", "PR", "IN"} {
				attr, _ := attributes[abbr].(map[string]any)
				g.Expect(attr).To(HaveKeyWithValue("temp", float64(50)), "attribute %s", abbr)
				g.Expect(attr).To(HaveKeyWithValue("pot", float64(50)), "attribute %s", abbr)
				g.Expect(attr).To(HaveKeyWithValue("bonus", float64(0)), "attribute %s", abbr)
			}

			// This Campaign never called the configure trigger, so its characterCreation.maxStatBudget
			// is still the engine's own CampaignCreated default (35) — see the Max Stat Budget design.
			g.Expect(stats["statBudget"]).To(Equal(float64(35)))
		}, time.Minute, time.Second).Should(Succeed())
```

Everything else in this `It` (the `addTrait` rejection/success checks after this block) is
unchanged.

`HaveKeyWithValue` is part of `github.com/onsi/gomega`, already dot-imported in this file — no
import changes needed. Confirm `querygen.Character`'s `Info` field name is still `Info` against the
live generated file before finalizing (already confirmed correct as of the Character Traits work;
re-verify in case it drifted).

- [ ] **Step 2: Run against a real cluster**

`make dev-up` then `make test-e2e` (or however this repo's e2e suite is normally invoked in the
implementing environment).
Expected: PASS, alongside every pre-existing `It` in this file.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/e2e_test.go
git commit -m "test/e2e: cover the real Character attributes/statBudget seeding against a live cluster"
```

---

## Final Verification

- `go build ./... && go vet ./... && go test -count=1 ./...` clean.
- `npm run build` clean; full Playwright suite clean, including Task 2's new
  `character-attributes.spec.ts`.
- Full Go e2e suite clean against a real cluster, including Task 3's extended `It`.
- `TestAttributeBonus` (internal, white-box) passes alongside every existing
  `internal/engine/timadorus` test.
