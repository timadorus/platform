# Campaign Character-Creation Max Stat Budget Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add one new, editable Campaign-configuration setting — `characterCreation.maxStatBudget`
— seeded to `35` on every new "Timadorus"-ruleset Campaign, changeable from the SPA's Configuration
panel via the existing `configure` trigger mechanism.

**Architecture:** Reuses the existing `ConfigurationRequested`/`CampaignProcessor` trigger
mechanism end to end — no new event, no new command endpoint. `handleCampaignCreated` merges one
more default key; `handleConfigurationRequested` gains a payload-shape dispatch for a
`{"action":"setMaxStatBudget","value":<n>}` action, falling through to its existing
timestamp-append behavior for anything else (backward compatible, zero regression risk). The SPA
gets a new editable field in `ConfigurationPanel.vue` that calls the existing `configure` endpoint
and relies on the already-built change-feed to reflect the (possibly-no-op) result.

**Tech Stack:** Go, `pgx`, `oapi-codegen`/`openapi-typescript`, Vue 3 `<script setup>`, Playwright.

## Global Constraints

- Scope: "Timadorus" Ruleset only (case-insensitive match against `targetRulesetName`), matching
  the existing default-Traits merge exactly. Every other Ruleset's Campaigns are untouched.
- JSON keys are camelCase: `characterCreation`, `maxStatBudget` — no space-containing keys
  anywhere.
- No new event type, no new command endpoint, no new OpenAPI path. The only OpenAPI change is
  loosening `requestCampaignConfiguration`'s existing request-body schema.
- `handleConfigurationRequested`'s existing timestamp-append behavior for a non-matching payload
  (including the literal `{}` used by existing tests) must be completely unchanged — this is the
  fallback path for every payload that isn't a recognized `setMaxStatBudget` action.
- `go build ./... && go vet ./... && go test ./...` must stay clean after every task; `npm run
  build` and the full Playwright suite must stay clean after every SPA-touching task.
- Both `api/command/gen/server.gen.go` (`go generate ./api/...`) and `web/src/api/command.types.ts`
  (`npm run generate` in `web/`) must be regenerated from the one OpenAPI schema change — verify
  both, not just one.

---

### Task 1: `timadorus-engine` — seed and update the setting

**Files:**
- Modify: `internal/engine/timadorus/campaign_processor.go`
- Modify: `internal/engine/timadorus/campaign_processor_test.go`

**Interfaces:**
- Consumes: `CampaignProcessor.mutateConfiguration(ctx, tx, env, campaignID, mutate func(map[string]any)) error` (already exists, unchanged signature).
- Produces: no new exported symbols. `configureAction` is unexported, package-internal.

- [ ] **Step 1: Write the failing tests**

Add to `internal/engine/timadorus/campaign_processor_test.go`. First, extend the existing
`TestCampaignProcessor_CampaignCreated_MatchingRuleset_MergesDefaultTraits` test's decode struct
and assertion (don't duplicate the whole test — add to it) to also assert
`characterCreation.maxStatBudget`:

```go
	var decoded struct {
		Traits           []string `json:"traits"`
		CharacterCreation struct {
			MaxStatBudget int `json:"maxStatBudget"`
		} `json:"characterCreation"`
	}
```

(replacing the existing narrower `decoded` struct in that test), and after the existing traits
assertion loop add:

```go
	if decoded.CharacterCreation.MaxStatBudget != 35 {
		t.Fatalf("got maxStatBudget %d, want 35", decoded.CharacterCreation.MaxStatBudget)
	}
```

Then add four new, independent tests:

```go
func TestCampaignProcessor_ConfigurationRequested_SetMaxStatBudget_MatchingRuleset_UpdatesValue(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)

	publish, wait := runCampaignEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload: mustMarshal(t, events.ConfigurationRequested{
			Payload:    `{"action":"setMaxStatBudget","value":40}`,
			OccurredAt: time.Now().UTC(),
		}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&payload)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query configuration_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if payload == nil {
		t.Fatal("timed out waiting for a campaign.configuration_changed.v1 event")
	}

	var configEvent struct {
		Configuration string `json:"configuration"`
	}
	if err := json.Unmarshal(payload, &configEvent); err != nil {
		t.Fatalf("configuration_changed payload %s is not the expected shape: %v", payload, err)
	}

	var decoded struct {
		Configs           []string `json:"configs"`
		CharacterCreation struct {
			MaxStatBudget int `json:"maxStatBudget"`
		} `json:"characterCreation"`
	}
	if err := json.Unmarshal([]byte(configEvent.Configuration), &decoded); err != nil {
		t.Fatalf("configuration %q is not the expected shape: %v", configEvent.Configuration, err)
	}
	if decoded.CharacterCreation.MaxStatBudget != 40 {
		t.Fatalf("got maxStatBudget %d, want 40", decoded.CharacterCreation.MaxStatBudget)
	}
	if len(decoded.Configs) != 0 {
		t.Fatalf("got configs %v, want none (a recognized setMaxStatBudget action must not also append a timestamp)", decoded.Configs)
	}

	wait()
}

func TestCampaignProcessor_ConfigurationRequested_UnrecognizedPayload_StillAppendsTimestamp(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)

	publish, wait := runCampaignEngine(t, pool)

	occurredAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: occurredAt}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&payload)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query configuration_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if payload == nil {
		t.Fatal("timed out waiting for a campaign.configuration_changed.v1 event")
	}

	var configEvent struct {
		Configuration string `json:"configuration"`
	}
	if err := json.Unmarshal(payload, &configEvent); err != nil {
		t.Fatalf("configuration_changed payload %s is not the expected shape: %v", payload, err)
	}

	var decoded struct {
		Configs           []string `json:"configs"`
		CharacterCreation map[string]any `json:"characterCreation"`
	}
	if err := json.Unmarshal([]byte(configEvent.Configuration), &decoded); err != nil {
		t.Fatalf("configuration %q is not the expected shape: %v", configEvent.Configuration, err)
	}
	if len(decoded.Configs) != 1 || decoded.Configs[0] != occurredAt.Format(time.RFC3339Nano) {
		t.Fatalf("got configs %v, want [%q] (the pre-existing fallback behavior must be unaffected)", decoded.Configs, occurredAt.Format(time.RFC3339Nano))
	}
	if decoded.CharacterCreation != nil {
		t.Fatalf("got characterCreation %v, want none (this Campaign was never created via CampaignCreated in this test, and this payload isn't a setMaxStatBudget action)", decoded.CharacterCreation)
	}

	wait()
}

func TestCampaignProcessor_ConfigurationRequested_SetMaxStatBudget_NonMatchingRuleset_NoOp(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "SomethingElse")
	campaignID := createCampaign(t, pool, rulesetID)

	publish, wait := runCampaignEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload: mustMarshal(t, events.ConfigurationRequested{
			Payload:    `{"action":"setMaxStatBudget","value":40}`,
			OccurredAt: time.Now().UTC(),
		}),
	})

	time.Sleep(500 * time.Millisecond)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		campaignID, events.TypeConfigurationChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count configuration_changed events: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d campaign.configuration_changed.v1 events, want 0 (ruleset doesn't match)", count)
	}

	wait()
}

func TestCampaignProcessor_TraitsAndCharacterCreationAndConfigsCoexist(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := createRuleset(t, pool, "timadorus")
	ctx := context.Background()
	repo := campaignRepo(pool)
	c, err := campaign.New(uuid.New(), rulesetID, "Test Campaign", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("campaign.New: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save campaign: %v", err)
	}
	campaignID := c.AggregateID()

	publish, wait := runCampaignEngine(t, pool)

	// 1. CampaignCreated merges traits + the maxStatBudget default.
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       1,
		EventType:     events.TypeCampaignCreated,
		Payload: mustMarshal(t, events.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: c.UniverseID(), RulesetID: rulesetID,
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC(),
		}),
	})
	waitForConfigVersion := func(wantVersion int) map[string]any {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var raw []byte
			err := pool.QueryRow(context.Background(),
				`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2 AND version = $3`,
				campaignID, events.TypeConfigurationChanged, wantVersion,
			).Scan(&raw)
			if err == nil {
				var ev struct {
					Configuration string `json:"configuration"`
				}
				if uerr := json.Unmarshal(raw, &ev); uerr != nil {
					t.Fatalf("unmarshal configuration_changed: %v", uerr)
				}
				var config map[string]any
				if uerr := json.Unmarshal([]byte(ev.Configuration), &config); uerr != nil {
					t.Fatalf("unmarshal configuration %q: %v", ev.Configuration, uerr)
				}
				return config
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("query configuration_changed event: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for configuration_changed version %d", wantVersion)
		return nil
	}
	config := waitForConfigVersion(2) // version 1 is CampaignCreated itself; the merge raises version 2
	if config["traits"] == nil {
		t.Fatal("traits missing after CampaignCreated")
	}
	if config["characterCreation"] == nil {
		t.Fatal("characterCreation missing after CampaignCreated")
	}

	// 2. A setMaxStatBudget action updates characterCreation without disturbing traits.
	publish(bus.Envelope{
		GlobalSeq:     2,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload: mustMarshal(t, events.ConfigurationRequested{
			Payload:    `{"action":"setMaxStatBudget","value":50}`,
			OccurredAt: time.Now().UTC(),
		}),
	})
	config = waitForConfigVersion(3)
	cc, _ := config["characterCreation"].(map[string]any)
	if cc["maxStatBudget"] != float64(50) { // decoded via encoding/json into map[string]any: numbers are float64
		t.Fatalf("got maxStatBudget %v, want 50", cc["maxStatBudget"])
	}
	if config["traits"] == nil {
		t.Fatal("traits should survive the setMaxStatBudget update")
	}

	// 3. An unrecognized payload still appends a timestamp without disturbing the other two keys.
	occurredAt := time.Now().UTC()
	publish(bus.Envelope{
		GlobalSeq:     3,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       3,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: occurredAt}),
	})
	config = waitForConfigVersion(4)
	configs, _ := config["configs"].([]any)
	if len(configs) != 1 {
		t.Fatalf("got configs %v, want exactly 1 entry", configs)
	}
	if config["traits"] == nil || config["characterCreation"] == nil {
		t.Fatal("traits and characterCreation should both survive the configs append")
	}

	wait()
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/engine/timadorus/... -run 'TestCampaignProcessor_(CampaignCreated_MatchingRuleset_MergesDefaultTraits|ConfigurationRequested_SetMaxStatBudget|TraitsAndCharacterCreationAndConfigsCoexist)' -v`
Expected: every new/modified assertion FAILs — `maxStatBudget`/`characterCreation` don't exist yet
in the merged configuration, and the `setMaxStatBudget` action currently falls straight into the
timestamp-append path (so the "must not also append a timestamp" assertion in the first new test
also fails).

- [ ] **Step 3: Implement**

In `internal/engine/timadorus/campaign_processor.go`, add below `defaultTraits`:

```go
// defaultMaxStatBudget's sibling to defaultTraits: character-creation defaults merged into every
// newly created Campaign that uses the "timadorus" Ruleset, alongside defaultTraits. Not a
// `const` for the same reason defaultTraits isn't (Go has no map constants); edit in place to
// change the starting value for new "timadorus" Campaigns.
var defaultMaxStatBudget = 35
```

Change `handleCampaignCreated`'s mutate closure from:

```go
	return p.mutateConfiguration(ctx, tx, env, campaignID, func(config map[string]any) {
		config["traits"] = defaultTraits
	})
```

to:

```go
	return p.mutateConfiguration(ctx, tx, env, campaignID, func(config map[string]any) {
		config["traits"] = defaultTraits
		config["characterCreation"] = map[string]any{"maxStatBudget": defaultMaxStatBudget}
	})
```

Replace `handleConfigurationRequested` entirely:

```go
// configureAction is the one recognized shape of a PUT .../configure payload today — everything
// else (including the empty {} the CLI's generic `action` verb and this package's own tests send)
// falls through to the pre-existing timestamp-append behavior below. Extend this dispatch, not
// the fallback, when the next real action is added.
type configureAction struct {
	Action string `json:"action"`
	Value  int    `json:"value"`
}

// handleConfigurationRequested mirrors handleCampaignCreated's shape. A recognized
// {"action":"setMaxStatBudget","value":n} payload updates characterCreation.maxStatBudget;
// any other payload (including the historical {} used by the CLI's generic action verb) appends
// occurredAt to the "configs" array instead, exactly as before. Neither mutation is idempotent
// under event replay (see mutateConfiguration's own doc comment).
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

Update the doc comments on the `CampaignProcessor` type (lines currently describing exactly two
effects — "merging default traits" and "appending a timestamp") to mention the new merged key and
the new action-dispatch branch, so they stay accurate.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/engine/timadorus/... -v`
Expected: PASS, all tests in the package including every pre-existing one (no regressions).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/timadorus/campaign_processor.go internal/engine/timadorus/campaign_processor_test.go
git commit -m "engine: seed and update characterCreation.maxStatBudget on Timadorus Campaigns"
```

---

### Task 2: Loosen `requestCampaignConfiguration`'s request body and regenerate both clients

**Files:**
- Modify: `api/command/openapi.yaml`
- Regenerate: `api/command/gen/server.gen.go`
- Regenerate: `web/src/api/command.types.ts`

**Interfaces:**
- Produces: `command.types.ts`'s `requestCampaignConfiguration.requestBody.content["application/json"]`
  changes from `Record<string, never>` to `Record<string, unknown>` — Task 3's `useCampaigns.ts`
  code depends on this to compile without a type assertion.

- [ ] **Step 1: Edit the schema**

In `api/command/openapi.yaml`, find the `/campaigns/{campaignId}/configure` path's `put.requestBody`
and change:

```yaml
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
```

to:

```yaml
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              additionalProperties: true
```

- [ ] **Step 2: Regenerate the Go server**

Run: `go generate ./api/...`
Expected: `git diff api/command/gen/server.gen.go` shows no change, or at most a trivial comment
diff — `RequestCampaignConfigurationJSONBody` was already `map[string]interface{}` before this
change (Go's codegen already treated a bare `type: object` as fully free-form).

- [ ] **Step 3: Regenerate the TypeScript client**

From `web/`, run: `npm run generate`
Expected: `git diff web/src/api/command.types.ts` shows exactly one change —
`requestCampaignConfiguration`'s request body content type changes from `Record<string, never>` to
`Record<string, unknown>`. No other type in the file changes (confirms the OpenAPI edit didn't
accidentally touch anything else, and that the file wasn't already stale before this task).

- [ ] **Step 4: Verify**

Run: `go build ./... && go vet ./...` (Go side) and, from `web/`, `npm run build` (TypeScript side).
Expected: both clean.

- [ ] **Step 5: Commit**

```bash
git add api/command/openapi.yaml api/command/gen/server.gen.go web/src/api/command.types.ts
git commit -m "api/command: loosen requestCampaignConfiguration's body to a genuinely free-form object"
```

---

### Task 3: SPA — the Configuration panel's new editable field

**Files:**
- Modify: `web/src/composables/useCampaigns.ts`
- Modify: `web/src/components/campaign/ConfigurationPanel.vue`
- Modify: `web/src/views/CampaignOverviewPanel.vue`

**Interfaces:**
- Consumes: `command.types.ts`'s loosened `requestCampaignConfiguration` body type (Task 2).
- Produces: `useCampaigns().requestConfiguration(id: string, payload: Record<string, unknown>): Promise<void>` — no other file needs this yet, but name/signature matches this codebase's existing `rename`/`archive`-style command methods.

- [ ] **Step 1: Add `requestConfiguration` to `useCampaigns.ts`**

Add this function inside `useCampaigns()`, alongside the existing `rename`/`archive` methods:

```ts
  async function requestConfiguration(id: string, payload: Record<string, unknown>): Promise<void> {
    const { error: apiError } = await getCommandClient().PUT('/campaigns/{campaignId}/configure', {
      params: { path: { campaignId: id } },
      body: payload,
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to request configuration change.')
  }
```

Add `requestConfiguration` to the object returned at the end of `useCampaigns()`.

- [ ] **Step 2: Rewrite `ConfigurationPanel.vue`**

Replace the whole file:

```vue
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useCampaigns } from '@/composables/useCampaigns'
import ErrorBanner from '@/components/common/ErrorBanner.vue'

const props = defineProps<{ campaignId: string; configuration: string }>()
const { requestConfiguration } = useCampaigns()

// The Configuration field is an opaque JSON string set via PUT /campaigns/{id}/configure — it
// starts unset/empty until that endpoint is called at least once, which is not valid JSON, so
// pretty-printing is attempted defensively rather than assumed to always succeed.
const parsedConfiguration = computed<Record<string, any> | null>(() => {
  if (!props.configuration) return null
  try {
    return JSON.parse(props.configuration)
  } catch {
    return null
  }
})

const prettyPrinted = computed(() => {
  if (!parsedConfiguration.value) return props.configuration || null
  return JSON.stringify(parsedConfiguration.value, null, 2)
})

const currentMaxStatBudget = computed<number | null>(
  () => parsedConfiguration.value?.characterCreation?.maxStatBudget ?? null,
)

// Initialized once from whatever the Campaign's configuration already says — deliberately not
// kept in sync with currentMaxStatBudget afterward (see the watch below and this component's own
// :key="campaignId" at its call site), so an unrelated background refresh never overwrites what
// the user is mid-typing. Mirrors the rename-draft-loss fix already applied elsewhere in this
// codebase (BaseInfoTable.vue/ManageCampaignPanel.vue).
const maxStatBudgetInput = ref<number | null>(currentMaxStatBudget.value)

type Status = 'idle' | 'pending' | 'error'
const status = ref<Status>('idle')
const errorMessage = ref<string | null>(null)
// The value most recently submitted, so the watch below can tell "the loaded configuration now
// reflects my own request" apart from "someone else changed something unrelated in configuration".
let pendingValue: number | null = null

async function save() {
  if (maxStatBudgetInput.value === null) return
  status.value = 'pending'
  errorMessage.value = null
  pendingValue = maxStatBudgetInput.value
  try {
    await requestConfiguration(props.campaignId, { action: 'setMaxStatBudget', value: maxStatBudgetInput.value })
  } catch (err) {
    status.value = 'error'
    errorMessage.value = err instanceof Error ? err.message : 'Failed to request configuration change.'
  }
}

// Fire-and-forget by design (see the design spec): a 204 from PUT .../configure only means the
// request was accepted, not that the value actually changed (e.g. a non-Timadorus Campaign
// silently no-ops). Only clear the pending status once the loaded configuration actually reflects
// what was submitted — driven by CampaignOverviewPanel.vue's existing lastAggregateChange watch
// reloading the Campaign, which is what updates this component's `configuration` prop.
watch(currentMaxStatBudget, (value) => {
  if (status.value === 'pending' && pendingValue !== null && value === pendingValue) {
    status.value = 'idle'
    pendingValue = null
  }
})
</script>

<template>
  <div class="rounded-md border border-slate-200 p-4">
    <h2 class="mb-3 text-sm font-semibold text-slate-900">Configuration</h2>

    <div class="mb-4 rounded-md border border-slate-100 bg-slate-50 p-3">
      <label class="mb-1 block text-xs font-medium text-slate-600">Max Stat Budget</label>
      <div class="flex items-center gap-2">
        <input
          v-model.number="maxStatBudgetInput"
          type="number"
          class="w-24 rounded-md border border-slate-300 px-2 py-1 text-sm"
        />
        <button
          class="text-xs text-indigo-600 hover:underline disabled:cursor-not-allowed disabled:text-slate-300"
          :disabled="maxStatBudgetInput === null || status === 'pending'"
          @click="save"
        >
          Save
        </button>
        <span v-if="status === 'pending'" class="text-xs text-slate-400">Update requested — refreshing…</span>
      </div>
      <ErrorBanner v-if="status === 'error'" :message="errorMessage" @dismiss="status = 'idle'" />
    </div>

    <p v-if="!prettyPrinted" class="text-sm text-slate-500">No configuration set yet.</p>
    <pre v-else class="overflow-x-auto rounded-md bg-slate-50 p-3 text-xs text-slate-800">{{ prettyPrinted }}</pre>
  </div>
</template>
```

- [ ] **Step 3: Wire `campaignId` and `:key` in `CampaignOverviewPanel.vue`**

Change:

```vue
    <ConfigurationPanel v-else-if="activeTab === 'Configuration'" :configuration="campaign.configuration" />
```

to:

```vue
    <ConfigurationPanel
      v-else-if="activeTab === 'Configuration'"
      :key="campaignId"
      :campaign-id="campaignId"
      :configuration="campaign.configuration"
    />
```

- [ ] **Step 4: Verify**

From `web/`, run: `npm run build`.
Expected: clean (this also typechecks the new prop/composable usage against Task 2's regenerated
types).

- [ ] **Step 5: Commit**

```bash
git add web/src/composables/useCampaigns.ts web/src/components/campaign/ConfigurationPanel.vue web/src/views/CampaignOverviewPanel.vue
git commit -m "web: add an editable Max Stat Budget field to the Configuration panel"
```

---

### Task 4: e2e coverage for the SPA flow

**Files:**
- Modify: `web/e2e/support/mockBackend.ts`
- Create: `web/e2e/campaign-configuration.spec.ts`

**Interfaces:**
- Consumes: `MockState.campaigns` (`MockCampaign[]`, already has `configuration?: string`).
- Produces: nothing consumed by other tasks.

This test cannot exercise the real `timadorus-engine`'s merge logic (that's Task 1's Go tests) —
it proves the SPA's own Save/pending/refresh flow: submitting a new value calls the right endpoint
with the right payload, shows the pending status, and the panel picks up the new value once the
mock's own change-feed reports a `campaign` change (simulating what the real engine's async
mutation + change-feed would eventually produce).

- [ ] **Step 1: Add a mock route for the `configure` trigger**

`mockBackend.ts` has no route for `PUT /api/command/campaigns/{campaignId}/configure` yet — add
one. Find the existing `PATCH /api/command/campaigns/:campaignId` (rename) route for the
insertion point and pattern to match, then add:

```ts
    if (method === 'PUT' && (m = matchPath('/api/command/campaigns/:campaignId/configure', p))) {
      // Mirrors the real backend's fire-and-forget semantics: accept the request, but only
      // actually mutate `configuration` (and record a change-feed row) if the test's own mock
      // state's campaign has a matching setup — tests that want to see the eventual effect call
      // applyConfigureAction (or push directly into state.campaigns/state.changes) themselves,
      // matching how universe-change-feed.spec.ts already injects an externally-made change.
      return route.fulfill({ status: 204, body: '' })
    }
```

(Exact placement/context matched against the live file by whoever implements this — the important
part is the route exists and 204s, matching the real endpoint's fire-and-forget contract. Do not
have this mock route synchronously mutate `state.campaigns` — the whole point of this test is
proving the SPA doesn't assume synchronous success.)

- [ ] **Step 2: Write the test**

Create `web/e2e/campaign-configuration.spec.ts`:

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
        configuration: JSON.stringify({ characterCreation: { maxStatBudget: 35 } }),
      },
    ],
    rulesets: [{ id: 'r1', name: 'Timadorus' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    creatorIds: ['user-1'],
    ...overrides,
  })
}

test('changing Max Stat Budget sends the setMaxStatBudget action and reflects the updated value once it lands', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  const apiCalls = await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')
  await page.getByRole('tab', { name: 'Configuration' }).click()

  const budgetInput = page.locator('input[type="number"]')
  await expect(budgetInput).toHaveValue('35')

  await budgetInput.fill('45')
  await page.getByRole('button', { name: 'Save' }).click()

  // 1. the request actually fired
  expect(apiCalls).toContain('PUT /api/command/campaigns/c1/configure')

  // 2. pending status shown — the mock's 204 doesn't itself change anything yet
  await expect(page.getByText('Update requested')).toBeVisible()

  // 3. simulate the async engine mutation + change-feed catching up: update the mock's own
  // recorded configuration and inject a change-feed row, exactly like universe-change-feed.spec.ts
  // does for an externally-made change.
  const campaign = state.campaigns.find((c) => c.id === 'c1')!
  campaign.configuration = JSON.stringify({ characterCreation: { maxStatBudget: 45 } })
  state.changes.push({
    globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
    universeId: 'u1',
    aggregateType: 'campaign',
    aggregateId: 'c1',
    eventType: 'campaign.configuration_changed.v1',
    occurredAt: new Date().toISOString(),
  })

  // 4. the panel picks it up via the existing change-feed poll and the pending status clears
  await expect(page.getByText('Update requested')).not.toBeVisible({ timeout: 10000 })
  await expect(budgetInput).toHaveValue('45')
})
```

The `state.changes.push` shape above matches `MockState.changes`'s real declared type exactly
(`{ globalSeq, universeId, aggregateType, aggregateId, eventType, occurredAt }` —
`web/e2e/support/mockBackend.ts:75`), confirmed against `universe-change-feed.spec.ts`'s own
existing injection code.

- [ ] **Step 3: Run**

From `web/`, run the full Playwright suite (matching this session's established
`LD_LIBRARY_PATH` workaround if the sandbox's `libnspr4.so` issue reproduces).
Expected: the new test passes alongside every pre-existing one.

- [ ] **Step 4: Commit**

```bash
git add web/e2e/support/mockBackend.ts web/e2e/campaign-configuration.spec.ts
git commit -m "test/e2e: cover the Configuration panel's Max Stat Budget save/refresh flow"
```

---

### Task 5: Real end-to-end coverage (Go, real cluster)

**Files:**
- Modify: `test/e2e/e2e_test.go`

**Interfaces:**
- Consumes: `querygen.Campaign` (already has `Configuration` per the design spec referenced in
  this file's own history), the package-level `env`/`doJSON` helpers already defined in this file.

`test/e2e/e2e_test.go` currently never exercises `PUT /campaigns/{id}/configure` or Campaign
configuration at all against a real cluster — this closes that gap for the one concrete case this
plan adds, proving the full real pipeline (command-api → timadorus-engine → query-api) works, not
just the mocked SPA flow (Task 4) and the isolated engine unit tests (Task 1).

- [ ] **Step 1: Add a new `It` block**

This needs a Campaign using the real "Timadorus" Ruleset — resolve it by name from `GET /rulesets`
first, matching the existing pattern from the ruleset-tables e2e coverage in this same file (search
for `"Timadorus"` in this file for that precedent), then create a fresh Campaign under it. Add,
inside the existing `Describe("Timadorus platform aggregates", ...)` block:

```go
	It("changing a Campaign's max stat budget via the configure trigger eventually updates its configuration", func() {
		var rulesets []querygen.Ruleset
		resp, err := doJSON(http.MethodGet, env.QueryAPIBaseURL+"/rulesets", env.BearerToken, nil, &rulesets)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var timadorusRuleset *querygen.Ruleset
		for i := range rulesets {
			if rulesets[i].Name == "Timadorus" {
				timadorusRuleset = &rulesets[i]
				break
			}
		}
		Expect(timadorusRuleset).NotTo(BeNil(), "expected timadorus-engine to have registered a Ruleset named \"Timadorus\" at startup")

		// Universe.New/Campaign.New both require at least one Creator/Gamemaster (ErrCreatorsRequired/
		// ErrGamemastersRequired) — a real User is needed, matching this file's own existing pattern
		// (see the giant "creates one of each aggregate" It above), not an empty slice.
		var user commandgen.UserCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/users", env.BearerToken,
			commandgen.CreateUserRequest{Name: "e2e-max-stat-budget-user"}, &user)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var universe commandgen.UniverseCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/universes", env.BearerToken,
			commandgen.CreateUniverseRequest{Name: "e2e-max-stat-budget-universe", CreatorUserIds: []uuid.UUID{user.Id}}, &universe)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var campaignResp commandgen.CampaignCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/campaigns", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateCampaignRequest{Name: "e2e-max-stat-budget-campaign", RulesetId: timadorusRuleset.Id, GamemasterUserIds: []uuid.UUID{user.Id}}, &campaignResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		// The default (35) should already be present once the engine's CampaignCreated handling
		// catches up.
		Eventually(func(g Gomega) {
			var got querygen.Campaign
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/campaigns/%s", env.QueryAPIBaseURL, campaignResp.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var config map[string]any
			g.Expect(json.Unmarshal([]byte(got.Configuration), &config)).To(Succeed())
			cc, _ := config["characterCreation"].(map[string]any)
			g.Expect(cc["maxStatBudget"]).To(Equal(float64(35)))
		}, time.Minute, time.Second).Should(Succeed())

		resp, err = doJSON(http.MethodPut, fmt.Sprintf("%s/campaigns/%s/configure", env.CommandAPIBaseURL, campaignResp.Id), env.BearerToken,
			map[string]any{"action": "setMaxStatBudget", "value": 50}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))

		Eventually(func(g Gomega) {
			var got querygen.Campaign
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/campaigns/%s", env.QueryAPIBaseURL, campaignResp.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var config map[string]any
			g.Expect(json.Unmarshal([]byte(got.Configuration), &config)).To(Succeed())
			cc, _ := config["characterCreation"].(map[string]any)
			g.Expect(cc["maxStatBudget"]).To(Equal(float64(50)))
		}, time.Minute, time.Second).Should(Succeed())
	})
```

Adjust `CreateUniverseRequest`/`CreateCampaignRequest`'s exact field names against this file's own
existing usages elsewhere (they're used identically earlier in this same file — copy the real
call shapes rather than guessing) and add `"encoding/json"` to the file's imports if not already
present.

- [ ] **Step 2: Run against a real cluster**

Follow this repo's existing e2e-run convention (`make dev-up` then `make test-e2e`, or however the
existing suite is normally invoked in the environment doing this work).
Expected: PASS, alongside every pre-existing `It` in this file.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/e2e_test.go
git commit -m "test/e2e: cover the real setMaxStatBudget round trip against a live cluster"
```

---

## Final Verification

- `go build ./... && go vet ./... && go test -count=1 ./...` clean.
- `npm run build` clean; full Playwright suite clean, including Task 4's new test.
- Full Go e2e suite clean against a real cluster, including Task 5's new `It`.
- `git diff api/command/gen/server.gen.go web/src/api/command.types.ts` confirms both were
  regenerated from the same schema change with no other drift.
