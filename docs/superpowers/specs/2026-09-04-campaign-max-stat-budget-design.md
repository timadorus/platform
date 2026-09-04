# Campaign Character-Creation Max Stat Budget — Design

## Context

Adds one new, editable Campaign-configuration setting — a numeric "max stat budget" used during
Character creation — reusing this platform's existing `configuration`/`configure` trigger
mechanism end to end rather than adding new endpoints, events, or schema. This is the exact
mechanism `timadorus-engine`'s `CampaignProcessor` already uses to merge default Traits into a new
Campaign's configuration, and to append a timestamp on a generic `configure` request — this spec
extends both existing code paths by one more case each, and adds the SPA UI that finally gives the
`configure` trigger a real caller (today, nothing in the SPA calls it; only the CLI's generic
`action` verb and this feature's own tests exercise it).

## Decisions

- **Scope: "Timadorus" Ruleset only**, matching the existing default-Traits merge exactly (same
  `strings.EqualFold(rulesetName, targetRulesetName)` gate, same `RulesetCache` resolution). A
  Campaign on any other Ruleset never gets this key seeded and any `setMaxStatBudget` action sent
  to it silently no-ops — identical behavior to how such a Campaign already ignores the existing
  Traits merge and the timestamp-append today.

- **JSON key names are camelCase** (`characterCreation`, `maxStatBudget`), matching every other
  field in this codebase's JSON payloads (`rulesetId`, `playerUserId`, `displayName`, etc.) — not
  the space-containing keys from the original ask, which would have been the only such keys in the
  platform.

- **No new event, no new endpoint, no new OpenAPI schema beyond one loosened field.** The SPA's
  "set max stat budget" action is a JSON payload sent to the *already-existing*
  `PUT /campaigns/{campaignId}/configure` (`requestCampaignConfiguration`) endpoint — the same
  `ConfigurationRequested` trigger `CampaignProcessor.handleConfigurationRequested` already
  consumes. The payload is `{"action": "setMaxStatBudget", "value": <number>}`.
  `handleConfigurationRequested` is extended to recognize this specific action shape and merge
  `value` into `configuration.characterCreation.maxStatBudget` instead of (not in addition to)
  appending a timestamp to `configuration.configs`. Any payload that doesn't match this shape
  (including the existing tests' `{}`) falls through to the current timestamp-append behavior
  completely unchanged — zero regression risk, and the trigger endpoint stays genuinely
  extensible for whatever the next action turns out to be, exactly as its own summary already
  promises ("may or may not act on it, asynchronously").

- **Default seeded at Campaign creation, no backfill** — `35` is merged into
  `configuration.characterCreation.maxStatBudget` inside `handleCampaignCreated`'s existing
  Traits-merge branch, the same one-time, forward-only seeding every other default-on-create value
  in this codebase already uses (matches `defaultTraits`'s own precedent exactly, including "no
  backfill for pre-existing Campaigns" being an accepted, already-documented gap in
  `docs/BACKLOG.md`).

- **Fire-and-forget is surfaced honestly in the UI, not hidden behind a fake "Saved."** A 204 from
  `PUT .../configure` only means the request was accepted, not that the value actually changed
  (e.g., a non-Timadorus Campaign silently no-ops). The SPA shows "Update requested — refreshing…"
  after Save, and clears that status only once the panel's own reload — already triggered by the
  existing `lastAggregateChange` watch in `CampaignOverviewPanel.vue` reacting to the
  `ConfigurationChanged` event this mutation produces — shows the loaded value actually matching
  what was submitted. No new polling mechanism; this reuses the change-feed infrastructure already
  built and merged this session.

- **The input's local value is never reactively overwritten by an unrelated background refresh**,
  mirroring the rename-draft-loss fix already applied to `BaseInfoTable.vue`/
  `ManageCampaignPanel.vue` earlier this session. `ConfigurationPanel.vue` gets a `:key="campaignId"`
  at its call site (matching `BaseInfoTable`'s own `:key="character.id"` precedent in
  `CharacterDetailView.vue`) so switching Campaigns remounts it fresh, rather than needing manual
  reset-on-campaign-switch logic inside the component.

- **`requestCampaignConfiguration`'s request-body schema is loosened from an implicitly-empty
  object to an explicitly free-form one.** `api/command/openapi.yaml`'s bare `type: object` (no
  `properties`, no `additionalProperties`) already generates `map[string]interface{}` on the Go
  side (genuinely free-form, matching the endpoint's own design intent), but `openapi-typescript`
  renders the identical schema as `Record<string, never>` — a strict *empty*-object type that
  would reject the `{action, value}` body this feature needs to send. Adding
  `additionalProperties: true` fixes the TypeScript side to `Record<string, unknown>` without
  changing the Go side at all. Both `api/command/gen/server.gen.go` and
  `web/src/api/command.types.ts` must be regenerated from this one schema change — the exact
  "regenerate both directions" discipline this session has already had to catch as a real gap on
  more than one earlier branch.

- **No validation of `value` beyond what JSON decoding already gives for free.** A malformed
  `value` (non-numeric) fails the action-shape unmarshal and silently falls through to the
  timestamp-append path instead of applying the intended change — an accepted trade-off matching
  this codebase's existing self-healing philosophy for malformed/unexpected trigger payloads
  (`mutateConfiguration`'s own doc comment: an unparseable existing `Configuration` string starts
  fresh rather than erroring, for the same reason). The SPA is the only real caller and always
  sends a well-formed payload, so this only matters for a hand-crafted request via the CLI's
  generic `action` verb or `curl`.

## Changes

### `internal/engine/timadorus/campaign_processor.go`

`defaultTraits` gains a sibling constant-ish var (or the merge closure gains one more line —
either way, one new top-level key alongside `"traits"`):

```go
// defaultCharacterCreationConfig's sibling: character-creation defaults merged into every newly
// created Campaign that uses the "timadorus" Ruleset, alongside defaultTraits. Not a `const` for
// the same reason defaultTraits isn't (Go has no map constants); edit in place to change the
// starting value for new "timadorus" Campaigns.
var defaultMaxStatBudget = 35
```

`handleCampaignCreated`'s mutate closure:

```go
return p.mutateConfiguration(ctx, tx, env, campaignID, func(config map[string]any) {
    config["traits"] = defaultTraits
    config["characterCreation"] = map[string]any{"maxStatBudget": defaultMaxStatBudget}
})
```

`handleConfigurationRequested` gains action-shape detection before its existing fallback:

```go
// configureAction is the one recognized shape of a PUT .../configure payload today — everything
// else (including the empty {} the CLI's generic `action` verb and this package's own tests send)
// falls through to the pre-existing timestamp-append behavior below. Extend this dispatch, not
// the fallback, when the next real action is added.
type configureAction struct {
    Action string `json:"action"`
    Value  int    `json:"value"`
}

func (p *CampaignProcessor) handleConfigurationRequested(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
    var e events.ConfigurationRequested
    if err := json.Unmarshal(env.Payload, &e); err != nil {
        return fmt.Errorf(errPrefix+"unmarshal %s: %w", env.EventType, err)
    }

    campaignID := env.AggregateID
    rulesetName, err := p.cache.resolve(ctx, tx, campaignID)
    if err != nil {
        return err
    }
    if !strings.EqualFold(rulesetName, targetRulesetName) {
        return nil // not our ruleset — no-op, still checkpointed as handled
    }

    var action configureAction
    if err := json.Unmarshal([]byte(e.Payload), &action); err == nil && action.Action == "setMaxStatBudget" {
        return p.mutateConfiguration(ctx, tx, env, campaignID, func(config map[string]any) {
            cc, _ := config["characterCreation"].(map[string]any)
            if cc == nil {
                cc = map[string]any{}
            }
            cc["maxStatBudget"] = action.Value
            config["characterCreation"] = cc
        })
    }

    occurredAt := e.OccurredAt
    return p.mutateConfiguration(ctx, tx, env, campaignID, func(config map[string]any) {
        configs, _ := config["configs"].([]any)
        config["configs"] = append(configs, occurredAt.UTC().Format(time.RFC3339Nano))
    })
}
```

Doc comments on the type (`CampaignProcessor`), `handleCampaignCreated`, and
`handleConfigurationRequested` need small updates to mention the new merged key and the new
action-dispatch branch — they currently describe exactly two effects each ("merging default
traits" / "appending a timestamp"), which becomes inaccurate once this lands.

### `internal/engine/timadorus/campaign_processor_test.go`

New tests, mirroring the existing Traits/configs ones exactly:
- `handleCampaignCreated` on a "timadorus" Campaign seeds
  `configuration.characterCreation.maxStatBudget == 35` alongside the existing Traits assertion
  (extend the existing test rather than duplicate its setup).
- `handleConfigurationRequested` with payload `{"action":"setMaxStatBudget","value":40}` on a
  "timadorus" Campaign sets `configuration.characterCreation.maxStatBudget == 40` and leaves
  `configs`/`traits` untouched (mirrors the existing "traits should survive the configs append"
  test's own shape).
- `handleConfigurationRequested` with the existing `{}` payload still appends a timestamp and does
  NOT touch `characterCreation` — proves the fallback path is unaffected (regression guard).
- `handleConfigurationRequested` with `{"action":"setMaxStatBudget","value":40}` on a
  non-"timadorus" Campaign is a no-op (mirrors the existing ruleset-gating tests' shape).

### `api/command/openapi.yaml`

```yaml
  /campaigns/{campaignId}/configure:
    put:
      operationId: requestCampaignConfiguration
      summary: >
        Request configuration of a Campaign. Does not change the Campaign directly — raises a
        ConfigurationRequested event that timadorus-engine may or may not act on, asynchronously.
      parameters:
        - $ref: "#/components/parameters/CampaignId"
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              additionalProperties: true
```

Regenerate `api/command/gen/server.gen.go` (`go generate ./api/...`) and
`web/src/api/command.types.ts` (`npm run generate` in `web/`) from this one change. Confirm the Go
side's diff is empty or near-empty (the underlying Go type was already `map[string]interface{}`)
and the TS side's `requestCampaignConfiguration.requestBody.content["application/json"]` changes
from `Record<string, never>` to `Record<string, unknown>`.

### `web/src/composables/useCampaigns.ts`

```ts
async function requestConfiguration(id: string, payload: Record<string, unknown>): Promise<void> {
  const { error: apiError } = await getCommandClient().PUT('/campaigns/{campaignId}/configure', {
    params: { path: { campaignId: id } },
    body: payload,
  })
  if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to request configuration change.')
}
```

Added to the returned object alongside the existing methods.

### `web/src/components/campaign/ConfigurationPanel.vue`

- New props: `campaignId: string` (needed for the Save call; `configuration` stays as-is).
- New parsed-once local state: `maxStatBudget` (a `ref<number | null>`, initialized from
  `JSON.parse(props.configuration).characterCreation?.maxStatBudget` on setup, defaulting to
  `null`/blank if absent or unparseable — reusing this file's existing defensive-parse pattern).
- New group rendered **above** the existing raw-JSON `<pre>` block: a label ("Max Stat Budget"), a
  `<input type="number">` bound to `maxStatBudget`, and a "Save" button that:
  1. Calls `requestConfiguration(props.campaignId, { action: 'setMaxStatBudget', value: maxStatBudget.value })`.
  2. Sets a local `status: Ref<'idle' | 'pending' | 'error'>` to `'pending'` and shows "Update
     requested — refreshing…" while pending.
  3. On error, sets `status` to `'error'` and shows the message via the existing `ErrorBanner`
     pattern this codebase already uses elsewhere.
- The pending status is cleared by the *parent* re-rendering this component with a `configuration`
  prop whose parsed `characterCreation.maxStatBudget` now equals what was submitted — handled via
  a `watch` on the parsed prop value inside `ConfigurationPanel.vue` itself (the component already
  re-parses `configuration` reactively for the existing `prettyPrinted` computed; this reuses that
  same parse).
- **Does not** reactively reset `maxStatBudget` from `props.configuration` after the initial parse
  — see the `:key="campaignId"` decision above for why that's safe without extra logic.

### `web/src/views/CampaignOverviewPanel.vue`

```vue
<ConfigurationPanel
  v-else-if="activeTab === 'Configuration'"
  :key="campaignId"
  :campaign-id="campaignId"
  :configuration="campaign.configuration"
/>
```

Only the `:key` and `:campaign-id` additions — everything else on this line is unchanged.

## Explicitly Out of Scope

- Any validation of `maxStatBudget`'s range (e.g., a minimum/maximum) — neither the domain nor the
  SPA validates this today; adding a floor/ceiling is a separate, later decision if it's ever
  needed.
- Surfacing `maxStatBudget` anywhere in the actual Character-creation flow (`CreateCharacterModal.vue`,
  stat allocation, etc.) — this spec only adds the setting and its Configuration-panel editor, not
  any enforcement of it during Character creation. That's a distinct, later feature.
- A CLI-specific convenience command for this action (e.g. `timadorusctl set max-stat-budget`) —
  the existing generic `timadorusctl action campaign <id> <jsonPayload>` already covers it with no
  new code.
- Backfilling `characterCreation.maxStatBudget` onto Campaigns created before this change ships —
  matches every other "no backfill" precedent already accepted in `docs/BACKLOG.md`.
