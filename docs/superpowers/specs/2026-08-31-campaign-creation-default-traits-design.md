# Campaign Creation: Default Traits via timadorus-engine — Design

## Context

`internal/engine/timadorus`'s `CampaignProcessor` already reacts asynchronously to Campaign
events, conditionally on that Campaign's Ruleset being "timadorus" (case-insensitive), mutating
the Campaign's opaque `configuration` string. Today it only reacts to `ConfigurationRequested`
(raised by `PUT /campaigns/{id}/configure`), appending the request's timestamp to a `"configs"`
JSON array inside `configuration`.

This spec adds a second trigger: on `CampaignCreated`, for a Campaign using the "timadorus"
Ruleset, merge `"traits": ["strong", "agile", "loyal"]` into that Campaign's `configuration`. The
default trait list lives as an editable Go value, not inline in the merge logic.

## Decisions

- **Ruleset gate:** only Campaigns using the "timadorus" Ruleset get default traits — this
  matches every other behavior `CampaignProcessor`/`CharacterProcessor` already implement, and
  keeps this engine's whole premise (a ruleset-specific reference implementation, no-op for any
  other Ruleset) intact rather than adding an unconditional exception to it.

- **Ruleset-name resolution avoids a real race.** `RulesetCache.resolve` (used today by the
  `ConfigurationRequested` path) looks up a Campaign's Ruleset name by joining
  `campaigns_read_model` to `rulesets_read_model` on campaign id. That join depends on
  `campaigns_read_model`'s own row already existing — written by a *different* projector
  (`internal/projection/campaign`) consuming the exact same `CampaignCreated` event this new
  handler reacts to, with no ordering guarantee between the two. Rather than depend on that race
  resolving favorably (or add ad-hoc retry), the new `CampaignCreated` handler resolves the
  Ruleset name directly from `rulesets_read_model` using `RulesetID`, which `CampaignCreated`
  already carries in its payload — no dependency on `campaigns_read_model` at all. The resolved
  name is cached under the campaign's id (via the existing `RulesetCache.set`), which also removes
  the same latent race for any `ConfigurationRequested` that arrives for that Campaign shortly
  after creation.

- **Default traits as an editable Go value, not a literal inline in the merge logic.** Go has no
  `const` for slice types, so this is an unexported package-level `var` in
  `campaign_processor.go`, alongside the existing `targetRulesetName` constant:
  ```go
  var defaultTraits = []string{"strong", "agile", "loyal"}
  ```
  Never mutated after initialization; editing the shipped defaults is a one-line change in one
  place.

- **Merge, not replace; overwrite, not append.** Like the existing `"configs"` handling, the new
  path parses the Campaign's current `configuration` into a generic `map[string]any` and touches
  only the `"traits"` key, leaving any other top-level key untouched (including a `"configs"`
  array from a prior `ConfigurationRequested`, and vice versa). Unlike `"configs"` (append-only,
  and explicitly documented as not idempotent under event replay), setting `"traits"` to the fixed
  default array is a plain overwrite — reprocessing `CampaignCreated` (e.g. after a checkpoint
  reset) converges to the same value rather than growing without bound.

- **Shared mutation plumbing.** The existing `appendConfigurationTimestamp` becomes a thin
  ruleset-specific wrapper (`configs` list append) around a new shared helper that owns the
  common "load Campaign → parse `configuration` into a map → apply a caller-supplied mutation to
  exactly the key(s) it owns → marshal → `SetConfiguration` → save, swallowing `ErrArchived`"
  sequence. The new default-traits path is a second, equally thin wrapper (plain key assignment)
  around the same helper. This removes the need to duplicate that ~15-line block a second time.

- **Archived-Campaign race:** a `CampaignCreated`-triggered mutation attempt on a Campaign that
  gets archived before this handler runs is vanishingly unlikely (archiving requires the Campaign
  to already be visible to a caller) but handled identically to the existing
  `ConfigurationRequested` path regardless: `SetConfiguration` returns `campaign.ErrArchived`,
  which the shared helper swallows as a clean no-op.

## Changes

### `internal/engine/timadorus/campaign_processor.go`

- Add `var defaultTraits = []string{"strong", "agile", "loyal"}` next to `targetRulesetName`.
- `Handle` dispatches on `env.EventType`: `events.TypeCampaignCreated` →
  `handleCampaignCreated`; `events.TypeConfigurationRequested` →
  `handleConfigurationRequested` (renamed from today's inline body); anything else → `nil`
  (unchanged fallthrough).
- `handleCampaignCreated` unmarshals `events.CampaignCreated`, resolves the Ruleset name via the
  new `RulesetCache.resolveByRulesetID(ctx, tx, campaignID, rulesetID)`, no-ops if it doesn't
  match "timadorus" (case-insensitive), and otherwise calls the shared mutation helper with a
  mutator that sets `config["traits"] = defaultTraits`.
- `handleConfigurationRequested` keeps today's `ConfigurationRequested`-unmarshal and
  `RulesetCache.resolve(ctx, tx, campaignID)` lookup unchanged, then calls the same shared
  mutation helper with a mutator that appends to `config["configs"]`.
- New shared helper (name: `mutateConfiguration`) takes the campaign id, the envelope (for
  correlation-id propagation), and a `func(map[string]any)` mutator; implements the "load → parse
  → mutate → marshal → `SetConfiguration` → save, swallow `ErrArchived`" sequence exactly as
  `appendConfigurationTimestamp` does today.

### `internal/engine/timadorus/cache.go`

- Add `RulesetCache.resolveByRulesetID(ctx, tx, campaignID, rulesetID uuid.UUID) (string, error)`:
  cache-hit on `campaignID` short-circuits exactly like `resolve`; on miss, queries
  `SELECT name FROM rulesets_read_model WHERE id = $1` using `rulesetID` (no join, no dependency
  on `campaigns_read_model`), then caches the result under `campaignID` via the existing `set`.
- Doc comment updated to describe both resolution paths and why `CampaignCreated` needs the
  id-direct one.

## Testing

Extend `internal/engine/timadorus/campaign_processor_test.go` (testcontainers-backed, same
`runCampaignEngine`/`seedRuleset`/`createCampaign` helpers already in place) with:

- **Matching ruleset → traits merged.** Seed a "timadorus"-named Ruleset, construct a Campaign via
  `campaign.New` (not `createCampaign`'s helper, which also seeds `campaigns_read_model` — this
  test asserts the new path works even when that row does *not* yet exist, proving the race fix),
  publish its real `CampaignCreated` envelope, and poll `events` for a resulting
  `campaign.configuration_changed.v1` whose `configuration` JSON has
  `"traits": ["strong","agile","loyal"]`.
- **Non-matching ruleset → no-op.** Same shape as `TestCampaignProcessor_NonMatchingRuleset_NoOp`,
  publishing `CampaignCreated` instead, asserting zero `ConfigurationChanged` events.
- **Traits and configs coexist.** Publish `CampaignCreated` (traits merged in), then publish
  `ConfigurationRequested` for the same Campaign, and assert the final `configuration` has both
  `"traits"` (untouched) and `"configs"` (the new timestamp) — proving the merge-not-replace
  behavior in both directions.
- **Archived-campaign race → clean no-op**, mirroring
  `TestCampaignProcessor_ArchivedCampaign_NoOp`: archive the Campaign immediately after creating
  it, then publish its `CampaignCreated` envelope, and assert zero `ConfigurationChanged` events
  and zero dead-letters.

## Explicitly Out of Scope

- Any SPA change — the Configuration tab already renders whatever JSON is present.
- Any change to `POST /universes/{universeId}/campaigns` or `internal/command/campaign`: this
  stays entirely inside the async engine, matching how `ConfigurationRequested` already works.
- Making `defaultTraits` configurable at runtime (env var, DB row, etc.) — the ask is a Go-source
  constant for easy editing, not runtime configurability.
- Any change to the "configs" timestamp-append behavior's own (pre-existing, documented)
  non-idempotency under replay.
