# Campaign Configuration + timadorus-engine Extension Design Spec

## Context

Mirrors the Character `info`/`action` feature (commits `81fe17b`..`b6bfad7`) onto Campaign: a
`configuration` field settable directly, and a `configure` trigger endpoint whose actual effect
is decided later, asynchronously, by `timadorus-engine` — conditionally on the Campaign's own
Ruleset name. No new binary/Dockerfile/Helm chart is needed; this extends the already-deployed
`timadorus-engine` to also consume Campaign events.

## 1. `configuration` field + direct-set endpoint

Full mirror of Character's `info`/`SetInfo`/`InfoChanged`, renamed to the domain vocabulary this
feature uses:

- `Campaign.configuration string` field, `Configuration() string` getter.
- `Campaign.SetConfiguration(value string) error` — archived-guarded, unconditional raise (no
  dedupe-if-unchanged, same as `SetInfo`/Ruleset's `SetDescription`).
- `events.ConfigurationChanged{Configuration string, OccurredAt time.Time}`,
  `TypeConfigurationChanged = "campaign.configuration_changed.v1"`.
- `PUT /campaigns/{campaignId}/configuration` (operationId `setCampaignConfiguration`), body
  `{ "configuration": "<json-string>" }` (schema `SetCampaignConfigurationRequest`, required
  `[configuration]`), `204`/`400`/`404`/`409` — no `422`, unconstrained content, exactly like
  `SetCharacterInfo`.
- Read side: `campaigns_read_model.configuration TEXT NOT NULL DEFAULT ''` (new migration),
  `query/campaign.Campaign.Configuration`, `api/query/openapi.yaml`'s `Campaign` schema gains
  `configuration` (required), `GetCampaign`/`ListCampaignsByUniverse` handlers include it.
- CLI: `timadorusctl set configuration <campaignId> <value>` → `PUT .../configuration`.

## 2. `configure` trigger endpoint

Full mirror of Character's `action`/`ActionRequested`/`RequestAction`:

- `events.ConfigurationRequested{Payload string, OccurredAt time.Time}`,
  `TypeConfigurationRequested = "campaign.configuration_requested.v1"`.
- `Campaign.RequestConfiguration(payload string) error` — archived-guarded, raises the event.
  `Apply`'s case for it is an explicit no-op (same rationale as Character's `RequestAction`: the
  actual effect, if any, is decided later and asynchronously by `timadorus-engine`, never by the
  aggregate itself).
- `PUT /campaigns/{campaignId}/configure` (operationId `requestCampaignConfiguration`), body
  `type: object` (free-form JSON, generates as `map[string]interface{}`, required), `204`/`400`/
  `404`/`409` — no `422`.
- CLI: reuses the existing `action` verb group (`timadorusctl action campaign <campaignId>
  <jsonPayload>` → `PUT .../configure`). `actionCmd`'s help text ("Request an action on an
  aggregate (PUT .../action)") is reworded to stop naming a single literal path, since it now
  covers two different ones (`.../action` for Character, `.../configure` for Campaign).

## 3. `timadorus-engine`: `CampaignProcessor`

- **Rename** the existing `Processor`/`ProcessorName` (`internal/engine/timadorus/processor.go`)
  to `CharacterProcessor`/`CharacterProcessorName` — an exported-symbol rename of already-merged
  code, done in the same commit that adds the sibling type, so nothing is left half-migrated.
  `NewProcessor` → `NewCharacterProcessor`.
- New `CampaignProcessor` in the same package (`campaign_processor.go`), implementing
  `projection.Projector` independently — one Processor per aggregate type, matching every
  existing convention (no single `Handle` dispatching across aggregate types).
- **Shares one `rulesetCache` instance** with `CharacterProcessor` — the cache is keyed by
  campaign id regardless of which aggregate's event triggered the lookup, so a Character-`action`
  and a Campaign-`configure` on the same campaign reuse the same cached name.
  `NewCharacterProcessor`/`NewCampaignProcessor` both gain a `cache *rulesetCache` parameter;
  `cmd/timadorus-engine/main.go` constructs one `rulesetCache` and passes it to both.
- `CampaignProcessor.Handle` reacts only to `ConfigurationRequested`. Its ruleset lookup is
  simpler than Character's: a Campaign event's own `env.AggregateID` *is* the campaign id, so it
  calls the exact same `rulesetName(ctx, tx, campaignID)` helper directly — no
  `characters_read_model` hop needed. `rulesetName` and `rulesetCache` move to a shared file
  (`ruleset_lookup.go`) since both processors now call them.
- On a match, `appendConfigurationTimestamp` mirrors `appendActionTimestamp`: loads the Campaign
  (`postgres.WithTx` + the originating envelope's correlation id, same as Character's), parses
  `Configuration()` into a generic map, appends the event's `OccurredAt` (RFC3339Nano) —
  **but under the key `"configs"`, not `"actions"`** (your correction; Character's `info` shape
  is unchanged, still `{"actions": [...]}`), and saves via `SetConfiguration`. Same archived-race
  handling as Character's (`errors.Is(err, campaign.ErrArchived)` → no-op, not an error).
- `cmd/timadorus-engine/main.go`'s `processors` slice gains `CampaignProcessor` alongside the
  renamed `CharacterProcessor`. No Dockerfile/Helm/devcluster changes — same binary, same
  Deployment, just subscribing to one more subject (`bus.Subject(campaignevents.AggregateType)`).

## Out of scope

- No changes to Character's `info`/`action` behavior or JSON shape.
- No new binary, image, or Helm template — `timadorus-engine` already exists and is already
  deployed; this only adds a second `Projector` registration to it.
- No shared "trigger endpoint" abstraction across Character/Campaign — each aggregate's
  `Request*`/`*Requested`/handler is written out per the existing copy-adapt convention already
  used throughout this codebase (e.g. `SetDescription`/`SetReferences` on Ruleset vs `SetInfo` on
  Character), not templated.

## Verification

- `go build ./...`, `go vet ./...`, `go test ./...` clean, including:
  - Domain unit tests for `SetConfiguration`/`RequestConfiguration` (mirroring Character's
    `TestSetInfo`/`TestRequestAction`/archived-guard coverage).
  - A testcontainers integration test for `CampaignProcessor` mirroring
    `processor_test.go`'s Character coverage: matching-ruleset appends a `"configs"` entry,
    non-matching-ruleset no-op, archived-Campaign no-op, correlation-id propagation.
  - Confirm the renamed `CharacterProcessor` symbols compile everywhere they're referenced
    (`cmd/timadorus-engine/main.go`) and its own existing tests still pass unchanged in behavior.
- Live, against the dev cluster (`make dev-up` — rebuilds/redeploys `timadorus-engine` with the
  new registration, no new image needed beyond that):
  1. `PUT /campaigns/{id}/configuration` sets the field directly; `GET` reflects it.
  2. For a Campaign whose Ruleset is named "Timadorus": `PUT /campaigns/{id}/configure` twice →
     `configuration` becomes `{"configs":["<ts1>","<ts2>"]}`.
  3. For a Campaign under a differently-named Ruleset: `configure` is a no-op.
  4. `configure` against an archived Campaign returns `409` from the command endpoint (the
     engine-side archived-race no-op is a separate, already-covered path).
  5. Confirm the shared `rulesetCache` still serves Character's own `action` correctly (no
     regression from the rename/shared-cache refactor) by repeating one of the existing
     Character live-verification scenarios.
