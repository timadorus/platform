# Character Traits — Design

## Context

Turns Character's currently-hardcoded, cosmetic "Traits" display into a real, server-validated,
actively-managed list: seeded at Character creation with a starting trait-point budget, editable
from the SPA via an "Add Trait" control that spends one point per trait, picking only from the
traits the Character's own Campaign already defines. Reuses this platform's existing
`info`/`action` trigger mechanism end to end (the same one `character-action-timadorus-engine`
already built and this session's Campaign `configuration`/`configure` work mirrored a second
time) — no new event type, no new command endpoint.

## Decisions

- **Scope: "Timadorus" Ruleset only**, matching every other `timadorus-engine` default-seeding and
  action-dispatch precedent in this codebase (`CampaignProcessor`'s default Traits/Max Stat
  Budget merges, `CharacterProcessor`'s own existing action-timestamp append) — same
  `strings.EqualFold(rulesetName, targetRulesetName)` gate throughout.

- **`CharacterCreated` gets a real handler for the first time.** `CharacterProcessor.Handle`
  today only reacts to `ActionRequested` — `CharacterCreated` is currently a pure no-op. On a
  "Timadorus"-ruleset Campaign, it now merges `{"stats": {"traitPoints": 2, "traits": []}}` into
  the new Character's `info`. Ruleset resolution still goes through
  `p.cache.resolve(ctx, tx, e.CampaignID)` — the exact same cache-then-read-model-join call the
  `ActionRequested` path already makes — but skips that path's extra `characters_read_model` hop,
  since `CharacterCreated`'s own payload already carries `CampaignID` directly (unlike
  `ActionRequested`, whose envelope only carries the Character's own id, requiring the extra join
  just to discover which Campaign it belongs to). `CharacterCreated` does **not** carry
  `RulesetID` the way `CampaignCreated` does, so — unlike `CampaignProcessor.handleCampaignCreated`'s
  event-store-based resolution — this cannot skip the read-model join on a cache miss; it always
  goes through `RulesetCache.resolve`, identically to the `ActionRequested` path.

- **`addTrait` is a recognized `ActionRequested` payload shape, dispatched the same way Campaign's
  `setMaxStatBudget` already is**: `{"action":"addTrait","trait":"<name>"}` sent to the existing
  `PUT /characters/{characterId}/action` trigger. Any other payload (including the historical `{}`
  used by existing tests) falls through to the pre-existing timestamp-append behavior, completely
  unchanged.

- **The engine — not the SPA — is the source of truth for whether a trait can be added.** On a
  recognized `addTrait` payload, the engine loads the Character's own Campaign's `configuration`
  (a new plain read-model query, `SELECT configuration FROM campaigns_read_model WHERE id = $1`,
  matching `RulesetCache.resolve`'s own already-established "cross-projection read-model lookup"
  pattern) and parses its `traits` array. The requested trait is applied — `stats.traitPoints`
  decremented (never below 0), the trait appended to `stats.traits` — only if **all** of:
  - it is a member of the Campaign's own configured `traits` list,
  - `stats.traitPoints > 0`,
  - it is not already present in the Character's own `stats.traits`.

  Once the action is recognized as `addTrait` (payload decodes with a non-empty `Action`/`Trait`),
  it commits to this branch regardless of outcome — it does **not** fall through to the
  timestamp-append fallback even when rejected, since that fallback is for a *different, not
  recognized* action, not a rejected one.

- **A rejected `addTrait` is logged, not silent.** `CharacterProcessor` gains a `*slog.Logger`
  field (it has none today — only `cmd/timadorus-engine/main.go` does), threaded through
  `NewCharacterProcessor`'s constructor. On rejection, `logger.Warn` records the character id,
  campaign id, the requested trait, and which specific check failed (not a Campaign trait / no
  points left / already has it) — a legitimate, expected outcome (e.g. the SPA's own picker
  showing a stale Campaign trait list), not an error, but worth surfacing for anyone debugging a
  report of "my Add Trait click didn't do anything."

- **The SPA is honest about the fire-and-forget nature of the request**, exactly like Campaign's
  Max Stat Budget field: a 204 from `PUT .../action` means "accepted," not "applied" — the engine
  can legitimately no-op (and now logs why). The UI shows a pending status that clears once the
  Character's own change-feed-driven reload shows the trait actually present, with the same
  timeout backstop so a rejected request can't strand the control forever.

- **`CharacterDetailView.vue`'s reload gets the same `silent`-option fix `CampaignOverviewPanel.vue`
  already got.** Its `load()` unconditionally nulls `character.value` before every reload,
  including change-feed-triggered ones — the exact bug the Campaign final review found and fixed.
  Left unfixed, the new pending-status mechanism here would be defeated the same way Campaign's
  was before that fix: any unrelated change to this Character (e.g. someone else renaming it)
  would remount the whole page and silently clear the pending indicator regardless of whether the
  trait was actually added.

- **The trait picker is a plain `<select>`, not a new reusable component** — mirrors
  `RulesetSelect.vue`'s shape (a short, small list, no search/filter needed) rather than
  `UserPicker.vue`'s (built for a potentially-long, searchable list). Options are the Campaign's
  own `configuration.traits` minus whatever the Character already has. `CharacterDetailView.vue`
  fetches the Campaign (`useCampaigns().get(character.campaignId)`) alongside its existing Character
  load, exactly the way `CampaignOverviewPanel.vue` already eagerly fetches sibling data (ruleset
  name, gamemasters) in one `load()`.

- **`requestCharacterAction`'s request-body schema needs the same loosening
  `requestCampaignConfiguration`'s did** — `api/command/openapi.yaml`'s bare `type: object` (no
  `additionalProperties`) generates `Record<string, never>` on the TypeScript side (a strict empty
  object, rejecting the `{action, trait}` body this feature needs to send), even though Go's side
  already treats it as fully free-form. Add `additionalProperties: true`, regenerate both
  `api/command/gen/server.gen.go` and `web/src/api/command.types.ts`.

## Changes

### `internal/engine/timadorus/character_processor.go`

```go
type CharacterProcessor struct {
	characters *eventsourcing.Repository[*character.Character]
	cache      *RulesetCache
	logger     *slog.Logger
}

func NewCharacterProcessor(pool *pgxpool.Pool, cache *RulesetCache, logger *slog.Logger) *CharacterProcessor {
	// ... unchanged construction, plus: logger: logger,
}

func (p *CharacterProcessor) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	switch env.EventType {
	case events.TypeCharacterCreated:
		return p.handleCharacterCreated(ctx, tx, env)
	case events.TypeActionRequested:
		return p.handleActionRequested(ctx, tx, env)
	default:
		return nil
	}
}
```

`handleCharacterCreated` resolves the ruleset via `p.cache.resolve(ctx, tx, e.CampaignID)` — the
same cache-then-read-model-join call the `ActionRequested` path already makes, just skipping that
path's extra `characters_read_model` hop since `e.CampaignID` is already known (see the Decisions
section above for why this can't use `CampaignProcessor.handleCampaignCreated`'s event-store-based
shortcut). It then merges the default `stats` object via a new `mutateInfo`-style helper mirroring
`CampaignProcessor.mutateConfiguration` exactly (load, parse-or-fresh, mutate, marshal, `SetInfo`,
save — the archived-race no-op included).

`handleActionRequested` (renamed from today's inline logic in `Handle`) keeps the existing
ruleset-gate-then-append-timestamp shape, with the `addTrait` dispatch inserted before the
fallback:

```go
type characterAction struct {
	Action string `json:"action"`
	Trait  string `json:"trait"`
}

// addTrait's specific rejection reasons, logged rather than silently dropped.
const (
	rejectNotACampaignTrait = "trait is not in the Campaign's own trait list"
	rejectNoPointsLeft      = "no traitPoints remaining"
	rejectAlreadyHasTrait   = "Character already has this trait"
)
```

The `addTrait` branch loads the Campaign's `configuration` via a small new helper
(`loadCampaignTraits(ctx, tx, campaignID) ([]string, error)`, a plain `SELECT configuration FROM
campaigns_read_model WHERE id = $1` + JSON-parse-`traits`, best-effort on parse failure matching
`mutateConfiguration`'s own established "start fresh rather than error" convention), checks the
three conditions, and either mutates `info` (via the same `mutateInfo` helper `handleCharacterCreated`
uses) or calls `p.logger.Warn(...)` with the character id, campaign id, trait, and rejection reason
— returning `nil` either way (no error, no fallback to timestamp-append).

### `cmd/timadorus-engine/main.go`

One-line change: `timadorusengine.NewCharacterProcessor(pool, cache, logger)` — the `logger` local
variable already exists in `run()`'s scope from `main()`'s own construction; it's simply threaded
one call deeper.

### `internal/engine/timadorus/character_processor_test.go`

New tests mirroring the existing `campaign_processor_test.go` shapes exactly:
- `CharacterCreated` on a Timadorus Campaign seeds `stats.traitPoints == 2` and `stats.traits == []`.
- `CharacterCreated` on a non-matching Ruleset is a no-op.
- `addTrait` for a trait that IS in the Campaign's `configuration.traits`, with `traitPoints > 0`
  and not already held, succeeds: decrements `traitPoints`, appends the trait.
- `addTrait` for a trait NOT in the Campaign's trait list is rejected (no mutation) — verify via a
  test log-capture (`slog.New(slog.NewTextHandler(&buf, nil))`) that a Warn line was emitted
  naming the trait and the rejection reason.
- `addTrait` when `traitPoints == 0` is rejected the same way.
- `addTrait` for a trait already in `stats.traits` is rejected the same way.
- An unrecognized `ActionRequested` payload (`{}`) still appends a timestamp unchanged — the
  existing regression guard, re-confirmed against the now-larger dispatch.

### `api/command/openapi.yaml`

```yaml
  /characters/{characterId}/action:
    put:
      # ... unchanged summary/parameters ...
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              additionalProperties: true
```

Regenerate `api/command/gen/server.gen.go` and `web/src/api/command.types.ts`.

### `web/src/composables/useCharacters.ts`

```ts
async function requestAction(id: string, payload: Record<string, unknown>): Promise<void> {
  const { error: apiError } = await getCommandClient().PUT('/characters/{characterId}/action', {
    params: { path: { characterId: id } },
    body: payload,
  })
  if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to request action.')
}
```

`CharacterSummary` is unchanged (`info` already added in the prior branch).

### `web/src/composables/useCampaigns.ts`

No change — `get(id)` already returns `configuration`.

### `web/src/views/CharacterDetailView.vue`

- `tabs` gains `'Configuration'` → `'Info'` rename.
- `load()` gains the same `silent` option `CampaignOverviewPanel.vue`'s did, used by the existing
  `lastAggregateChange` watch; a genuine character switch (`watch(characterId, load)`) stays
  non-silent.
- Also loads the Campaign (`useCampaigns().get(character.value.campaignId)`) inside `load()`,
  storing it in a new `campaign` ref, to source the picker's available-traits list.
- New `onSubmitAddTrait(trait: string)` handler mirroring `onSubmitReassignPlayer`'s shape exactly,
  calling `requestAction(character.value.id, { action: 'addTrait', trait })`.
- `BaseInfoTable` gains new props: `traits: string[]`, `trait-points: number`,
  `available-traits: string[]` (Campaign's traits minus the Character's own), and a new
  `@submit-add-trait="onSubmitAddTrait"` listener.

### `web/src/components/character/CharacterConfigurationPanel.vue`

Heading text `Configuration` → `Info` (component file name and props are unchanged — only what it
displays is still called "Info" now everywhere, matching the renamed tab).

### `web/src/components/character/BaseInfoTable.vue`

- Traits row now renders the real list (comma-joined, matching the current placeholder's own
  display style) or `(no traits selected)` when empty.
- When `traitPoints > 0`, an "Add Trait" button appears after the traits list. Clicking it reveals
  an inline `<select>` (options = `availableTraits`) plus a confirm button — mirroring
  `ManageCampaignPanel.vue`'s "+ Add" → reveal-picker → select interaction shape, adapted to a
  plain `<select>` instead of a search-filtered list.
- Same pending/timeout/error UX as `ConfigurationPanel.vue`'s Max Stat Budget field: `status: 'idle'
  | 'pending' | 'error'`, a `PENDING_TIMEOUT_MS` backstop, cleared only when the reloaded `traits`
  prop actually contains the submitted trait.

## Explicitly Out of Scope

- Any UI for a Campaign's own GM to change its `configuration.traits` list after creation — that
  list is already fixed at Campaign-creation time by the existing default-Traits feature; this
  spec only reads it.
- Removing a trait once added, or any way to regain spent `traitPoints` — not asked for.
- Any change to how `traitPoints`/`traits` interact with `AttributesTable.vue` (the Stats tab's
  other card) — this spec only touches `BaseInfoTable.vue`'s existing Traits row.
- Backfilling `stats.traitPoints`/`stats.traits` onto Characters created before this branch ships
  — matches every other "no backfill" precedent already accepted in `docs/BACKLOG.md`.
- Cross-checking a rejected `addTrait`'s specific reason back to the SPA (e.g. distinguishing
  "already have it" from "not in the Campaign's list" in the UI) — the SPA only ever offers
  eligible traits in its own picker, so a rejection it can't explain is already an edge case (a
  stale picker, a race with someone else's concurrent Add Trait); the pending-timeout's generic
  "no confirmation received" message is enough, matching Max Stat Budget's own treatment of the
  analogous case.
