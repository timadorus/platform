# Campaign Configuration + timadorus-engine Extension Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mirror Character's `info`/`action` feature onto Campaign — a `configuration` field
settable directly via `PUT /campaigns/{campaignId}/configuration`, and a
`PUT /campaigns/{campaignId}/configure` trigger endpoint whose effect is decided asynchronously
by `timadorus-engine`, conditionally on the Campaign's own Ruleset name — extending the
already-deployed `timadorus-engine` binary with a second, sibling processor rather than building
new infrastructure.

**Architecture:** Task 1 is a full vertical slice on Campaign (domain, command, HTTP, OpenAPI,
query read model, CLI) copy-adapted from Character's already-shipped, already-reviewed
equivalent. Task 2 splits `timadorus-engine`'s existing `Processor` into `CharacterProcessor` +
a new sibling `CampaignProcessor`, sharing one `RulesetCache` instance across both.

**Tech Stack:** Go, oapi-codegen (strict-server), Watermill/NATS JetStream, pgx, testcontainers-go.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-08-23-campaign-configuration-timadorus-engine-design.md`.
- Endpoint paths are exact: `PUT /campaigns/{campaignId}/configuration` (direct set) and
  `PUT /campaigns/{campaignId}/configure` (trigger) — not `/info`/`/action`.
- The Go field/method names use "Configuration", not "Info": `Configuration()`,
  `SetConfiguration(value string) error`, `RequestConfiguration(payload string) error`,
  `ConfigurationChanged`, `ConfigurationRequested`.
- `RequestConfiguration`'s `Apply` case is an explicit no-op, exactly like Character's
  `ActionRequested` — self-documenting, not omitted from the switch.
- The `configure` endpoint's request body is `type: object` with no `properties` (generates as
  `map[string]interface{}`), no `422`. The `configuration` endpoint's body is
  `{ "configuration": "<string>" }`, required, no `422`.
- **The appended list key is `"configs"`, not `"actions"`** — e.g.
  `{"configs":["2026-08-23T12:00:00.000000000Z"]}`. Character's `info` shape is completely
  unchanged by this plan.
- Timestamp format is `time.RFC3339Nano` (matching Character's current, already-fixed format —
  not the older plain `RFC3339` an earlier version of that feature briefly used).
- No new binary, Dockerfile, or Helm template — `cmd/timadorus-engine` already exists and is
  already deployed; this only adds a second `projection.Projector` registration to it.
- `internal/engine/timadorus`'s existing `Processor`/`ProcessorName`/`NewProcessor` are renamed
  to `CharacterProcessor`/`CharacterProcessorName`/`NewCharacterProcessor` in the same commit that
  adds `CampaignProcessor` — nothing is left half-migrated.
- `rulesetCache` is exported as `RulesetCache` (constructor `NewRulesetCache`) so
  `cmd/timadorus-engine/main.go` can construct one and inject it into both processors. Its
  cache-or-query logic becomes a method on `RulesetCache` itself (`resolve`), called identically
  by both processors — not duplicated.

---

### Task 1: Campaign `configuration` field + `configure` trigger — domain, command, HTTP, OpenAPI, query, CLI

**Files:**
- Modify: `internal/domain/campaign/events/events.go`
- Modify: `internal/domain/campaign/campaign.go`
- Modify: `internal/domain/campaign/campaign_test.go`
- Modify: `internal/command/campaign/service.go`
- Modify: `api/command/openapi.yaml`
- Modify: `api/command/gen/server.gen.go` (regenerated)
- Modify: `internal/httpapi/command/server.go`
- Create: `internal/projection/campaign/migrations/0003_campaign_configuration.up.sql`
- Create: `internal/projection/campaign/migrations/0003_campaign_configuration.down.sql`
- Modify: `internal/projection/campaign/projector.go`
- Modify: `internal/query/campaign/repository.go`
- Modify: `api/query/openapi.yaml`
- Modify: `api/query/gen/server.gen.go` (regenerated)
- Modify: `internal/httpapi/query/server.go`
- Modify: `internal/cliapp/root.go`
- Modify: `internal/cliapp/campaign.go`

**Interfaces:**
- Produces: `events.TypeConfigurationChanged`, `events.ConfigurationChanged{Configuration string, OccurredAt time.Time}`.
- Produces: `events.TypeConfigurationRequested`, `events.ConfigurationRequested{Payload string, OccurredAt time.Time}` (consumed by Task 2).
- Produces: `Campaign.Configuration() string`, `Campaign.SetConfiguration(value string) error`, `Campaign.RequestConfiguration(payload string) error`.
- Produces: `campaigncmd.Service.SetConfiguration(ctx, id uuid.UUID, value string) error`, `campaigncmd.Service.RequestConfiguration(ctx, id uuid.UUID, payload string) error`.
- Produces: `PUT /campaigns/{campaignId}/configuration` (204/400/404/409), `PUT /campaigns/{campaignId}/configure` (204/400/404/409).
- Produces: `campaigns_read_model.configuration` column, exposed via `GET /campaigns/{campaignId}` and `GET /universes/{universeId}/campaigns`.

- [ ] **Step 1: Add both events to `internal/domain/campaign/events/events.go`**

Change the `const` block:
```go
const (
	TypeCampaignCreated         = "campaign.created.v1"
	TypeCampaignRenamed         = "campaign.renamed.v1"
	TypeGamemasterAdded         = "campaign.gamemaster_added.v1"
	TypeGamemasterRemove        = "campaign.gamemaster_removed.v1"
	TypeConfigurationChanged    = "campaign.configuration_changed.v1"
	TypeConfigurationRequested  = "campaign.configuration_requested.v1"
	TypeCampaignArchived        = "campaign.archived.v1"
)
```

Add, after `GamemasterRemoved`'s type/method (before `type CampaignArchived struct`):
```go
// ConfigurationChanged carries the Campaign's new configuration wholesale (replace, not merge
// — same shape as domain/character's InfoChanged).
type ConfigurationChanged struct {
	Configuration string    `json:"configuration"`
	OccurredAt    time.Time `json:"occurredAt"`
}

func (ConfigurationChanged) EventType() string { return TypeConfigurationChanged }

// ConfigurationRequested is a pure trigger: raised by Campaign.RequestConfiguration, applied as
// a no-op (see Campaign.Apply) — the actual effect, if any, is decided later and asynchronously
// by timadorus-engine's CampaignProcessor, conditionally on this Campaign's own Ruleset name,
// never by the Campaign aggregate itself. Payload is the PUT /campaigns/{id}/configure request
// body, re-marshaled to a canonical JSON string (same representation Configuration itself uses).
type ConfigurationRequested struct {
	Payload    string    `json:"payload"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (ConfigurationRequested) EventType() string { return TypeConfigurationRequested }
```

Add to `Register`:
```go
func Register(reg *eventsourcing.Registry) {
	reg.Register(TypeCampaignCreated, func() eventsourcing.Event { return &CampaignCreated{} })
	reg.Register(TypeCampaignRenamed, func() eventsourcing.Event { return &CampaignRenamed{} })
	reg.Register(TypeGamemasterAdded, func() eventsourcing.Event { return &GamemasterAdded{} })
	reg.Register(TypeGamemasterRemove, func() eventsourcing.Event { return &GamemasterRemoved{} })
	reg.Register(TypeConfigurationChanged, func() eventsourcing.Event { return &ConfigurationChanged{} })
	reg.Register(TypeConfigurationRequested, func() eventsourcing.Event { return &ConfigurationRequested{} })
	reg.Register(TypeCampaignArchived, func() eventsourcing.Event { return &CampaignArchived{} })
}
```

- [ ] **Step 2: Add the field and both methods to `internal/domain/campaign/campaign.go`**

Add `configuration string` to the `Campaign` struct, after `gamemasters`:
```go
type Campaign struct {
	eventsourcing.Base

	name          string
	universeID    uuid.UUID
	rulesetID     uuid.UUID
	gamemasters   map[uuid.UUID]struct{}
	configuration string
	archived      bool
}
```

Add getters, after `HasGamemaster`:
```go
// Configuration is an opaque JSON-string payload (the backend never parses or validates it) —
// see SetConfiguration for the only command that changes it directly, and RequestConfiguration
// for the trigger that changes it indirectly via timadorus-engine.
func (c *Campaign) Configuration() string { return c.configuration }
```

Add both methods, after `RemoveGamemaster` (before `Archive`):
```go
// SetConfiguration replaces the Campaign's opaque configuration string wholesale (same
// "replace, don't merge" shape as character.Character.SetInfo — no minimum-content invariant to
// protect, so no dedupe-if-unchanged short-circuit).
func (c *Campaign) SetConfiguration(value string) error {
	if c.archived {
		return ErrArchived
	}
	c.raise(&events.ConfigurationChanged{Configuration: value, OccurredAt: time.Now().UTC()})
	return nil
}

// RequestConfiguration raises ConfigurationRequested without mutating any aggregate field (see
// Apply below) — the actual effect, if any, happens later and asynchronously in
// timadorus-engine's CampaignProcessor, and only conditionally on this Campaign's own Ruleset
// (see that package's own docs). Guarded by the same archived check every other mutating
// command uses.
func (c *Campaign) RequestConfiguration(payload string) error {
	if c.archived {
		return ErrArchived
	}
	c.raise(&events.ConfigurationRequested{Payload: payload, OccurredAt: time.Now().UTC()})
	return nil
}
```

Add two cases to `Apply`'s switch, after `case *events.GamemasterRemoved:`:
```go
	case *events.ConfigurationChanged:
		c.configuration = e.Configuration
	case *events.ConfigurationRequested:
		// Intentionally a no-op — see RequestConfiguration's doc comment. An explicit case
		// (rather than falling through to no case at all) keeps this switch exhaustive and
		// self-documenting.
```

- [ ] **Step 3: Add domain tests to `internal/domain/campaign/campaign_test.go`**

Add:
```go
func TestSetConfiguration(t *testing.T) {
	c, err := campaign.New(uuid.New(), uuid.New(), "Curse of Strahd", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.ClearPending()

	if err := c.SetConfiguration(`{"difficulty":"hard"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := c.Configuration(); got != `{"difficulty":"hard"}` {
		t.Fatalf("got configuration %q", got)
	}
	if got := len(c.Pending()); got != 1 {
		t.Fatalf("got %d pending events, want 1", got)
	}
}

func TestRequestConfiguration(t *testing.T) {
	c, err := campaign.New(uuid.New(), uuid.New(), "Curse of Strahd", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.ClearPending()

	nameBefore, universeBefore, rulesetBefore, configBefore := c.Name(), c.UniverseID(), c.RulesetID(), c.Configuration()

	if err := c.RequestConfiguration(`{"foo":"bar"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(c.Pending()); got != 1 {
		t.Fatalf("got %d pending events, want 1", got)
	}
	if c.Name() != nameBefore || c.UniverseID() != universeBefore || c.RulesetID() != rulesetBefore ||
		c.Configuration() != configBefore {
		t.Fatalf("RequestConfiguration must not mutate any field, but at least one changed")
	}
}
```

Add to `TestArchive`'s guard-list (after the existing `AddGamemaster` check):
```go
	if err := c.SetConfiguration("{}"); err != campaign.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
	if err := c.RequestConfiguration("{}"); err != campaign.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
```

- [ ] **Step 4: Run domain tests**

Run: `go test ./internal/domain/campaign/... -v`
Expected: all tests pass, including the new ones.

- [ ] **Step 5: Add both methods to `internal/command/campaign/service.go`**

Add, after `RemoveGamemaster`:
```go
func (s *Service) SetConfiguration(ctx context.Context, id uuid.UUID, value string) error {
	c, err := s.campaigns.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := c.SetConfiguration(value); err != nil {
		return err
	}
	return s.campaigns.Save(ctx, c)
}

func (s *Service) RequestConfiguration(ctx context.Context, id uuid.UUID, payload string) error {
	c, err := s.campaigns.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := c.RequestConfiguration(payload); err != nil {
		return err
	}
	return s.campaigns.Save(ctx, c)
}
```

- [ ] **Step 6: Add both OpenAPI paths to `api/command/openapi.yaml`**

Add, immediately after the existing `/campaigns/{campaignId}/gamemasters/{userId}` block (path
insertion order in this file doesn't affect behavior — this is just where the other
`/campaigns/{campaignId}/...` paths live):
```yaml
  /campaigns/{campaignId}/configuration:
    put:
      operationId: setCampaignConfiguration
      summary: Replace a Campaign's configuration (an opaque JSON string the backend never parses or validates).
      parameters:
        - $ref: "#/components/parameters/CampaignId"
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/SetCampaignConfigurationRequest"
      responses:
        "204":
          description: Updated.
        "400":
          $ref: "#/components/responses/BadRequest"
        "404":
          $ref: "#/components/responses/NotFound"
        "409":
          $ref: "#/components/responses/Conflict"

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
      responses:
        "204":
          description: Configuration requested.
        "400":
          $ref: "#/components/responses/BadRequest"
        "404":
          $ref: "#/components/responses/NotFound"
        "409":
          $ref: "#/components/responses/Conflict"
```

Add the request schema, immediately after `CampaignCreatedResponse`:
```yaml
    SetCampaignConfigurationRequest:
      type: object
      required: [configuration]
      properties:
        configuration:
          type: string
```

- [ ] **Step 7: Regenerate the command API's generated server code**

Run: `go generate ./api/command/...`
Expected: clean exit. Confirm with
`grep -n "SetCampaignConfiguration\|RequestCampaignConfiguration" api/command/gen/server.gen.go`
— both gain request/response types, a `StrictServerInterface` method, and a `.Methods(http.MethodPut)`
route registration. Do not hand-edit this file.

- [ ] **Step 8: Add both handlers to `internal/httpapi/command/server.go`**

Add, after the existing Campaign handlers (near `RemoveCampaignGamemaster`; `"encoding/json"` is
already imported in this file from the Character `action` work):
```go
func (s *Server) SetCampaignConfiguration(ctx context.Context, request gen.SetCampaignConfigurationRequestObject) (gen.SetCampaignConfigurationResponseObject, error) {
	if request.Body == nil {
		return gen.SetCampaignConfiguration400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.campaign.SetConfiguration(ctx, request.CampaignId, request.Body.Configuration); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.SetCampaignConfiguration404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.SetCampaignConfiguration409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.SetCampaignConfiguration400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.SetCampaignConfiguration204Response{}, nil
}

func (s *Server) RequestCampaignConfiguration(ctx context.Context, request gen.RequestCampaignConfigurationRequestObject) (gen.RequestCampaignConfigurationResponseObject, error) {
	if request.Body == nil {
		return gen.RequestCampaignConfiguration400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	payload, err := json.Marshal(*request.Body)
	if err != nil {
		return gen.RequestCampaignConfiguration400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", err)),
		}, nil
	}

	if err := s.campaign.RequestConfiguration(ctx, request.CampaignId, string(payload)); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.RequestCampaignConfiguration404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.RequestCampaignConfiguration409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.RequestCampaignConfiguration400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.RequestCampaignConfiguration204Response{}, nil
}
```

- [ ] **Step 9: Create the read-model migration**

`internal/projection/campaign/migrations/0003_campaign_configuration.up.sql`:
```sql
ALTER TABLE campaigns_read_model ADD COLUMN configuration TEXT NOT NULL DEFAULT '';
```

`internal/projection/campaign/migrations/0003_campaign_configuration.down.sql`:
```sql
ALTER TABLE campaigns_read_model DROP COLUMN configuration;
```

- [ ] **Step 10: Handle `ConfigurationChanged` in `internal/projection/campaign/projector.go`**

Add a case to `Handle`'s switch, after `case events.TypeGamemasterRemove:`:
```go
	case events.TypeConfigurationChanged:
		return p.handleConfigurationChanged(ctx, tx, env)
```

Add the handler function, after `handleGamemasterRemoved`:
```go
func (p *Projector) handleConfigurationChanged(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.ConfigurationChanged
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("campaign projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx,
		`UPDATE campaigns_read_model SET configuration = $2, updated_at = $3 WHERE id = $1`,
		env.AggregateID, e.Configuration, e.OccurredAt,
	)
	return err
}
```

`TypeConfigurationRequested` needs no case — it falls through to the existing `default: return nil`,
exactly like every other event type this projector doesn't own.

- [ ] **Step 11: Add `Configuration` to `internal/query/campaign/repository.go`**

Add to the `Campaign` struct:
```go
type Campaign struct {
	ID            uuid.UUID
	Name          string
	UniverseID    uuid.UUID
	RulesetID     uuid.UUID
	Configuration string
	IsArchived    bool
}
```

Update both `Get` and `ListByUniverse`'s SQL/Scan calls to include `configuration`:
```go
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, universe_id, ruleset_id, configuration, is_archived FROM campaigns_read_model WHERE id = $1`, id,
	).Scan(&c.ID, &c.Name, &c.UniverseID, &c.RulesetID, &c.Configuration, &c.IsArchived)
```
```go
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, universe_id, ruleset_id, configuration, is_archived FROM campaigns_read_model
		 WHERE universe_id = $1 AND is_archived = false
		 ORDER BY name`,
		universeID,
	)
	...
		if err := rows.Scan(&c.ID, &c.Name, &c.UniverseID, &c.RulesetID, &c.Configuration, &c.IsArchived); err != nil {
```

- [ ] **Step 12: Add `configuration` to `api/query/openapi.yaml`'s `Campaign` schema**

```yaml
    Campaign:
      type: object
      required: [id, name, universeId, rulesetId, configuration, isArchived]
      properties:
        id:
          type: string
          format: uuid
        name:
          type: string
        universeId:
          type: string
          format: uuid
        rulesetId:
          type: string
          format: uuid
        # configuration is an opaque JSON-string payload, never parsed or validated by the
        # backend. Settable directly via PUT /campaigns/{campaignId}/configuration, or
        # indirectly (as a {"configs":[...]} timestamp list) by timadorus-engine reacting to
        # PUT /campaigns/{campaignId}/configure — see api/command/openapi.yaml.
        configuration:
          type: string
        isArchived:
          type: boolean
```

- [ ] **Step 13: Regenerate the query API's generated server code**

Run: `go generate ./api/query/...`
Expected: clean exit. Confirm with `grep -n "Configuration" api/query/gen/server.gen.go` — the
`Campaign` struct gains a `Configuration string` field. Do not hand-edit this file.

- [ ] **Step 14: Update `internal/httpapi/query/server.go`'s `GetCampaign`/`ListCampaignsByUniverse`**

```go
	return gen.GetCampaign200JSONResponse{Id: c.ID, Name: c.Name, UniverseId: c.UniverseID, RulesetId: c.RulesetID, Configuration: c.Configuration, IsArchived: c.IsArchived}, nil
```
```go
		out[i] = gen.Campaign{Id: c.ID, Name: c.Name, UniverseId: c.UniverseID, RulesetId: c.RulesetID, Configuration: c.Configuration, IsArchived: c.IsArchived}
```

- [ ] **Step 15: Reword `actionCmd`'s help text in `internal/cliapp/root.go`**

It currently reads (hardcoding one literal path that's no longer accurate now that Campaign uses
a different one):
```go
		actionCmd: &cobra.Command{
			Use:   "action",
			Short: "Request an action on an aggregate (PUT .../action) — effect, if any, is asynchronous",
		},
```
Change to:
```go
		actionCmd: &cobra.Command{
			Use:   "action",
			Short: "Request an action/configuration on an aggregate — effect, if any, is asynchronous",
		},
```

- [ ] **Step 16: Add CLI commands to `internal/cliapp/campaign.go`**

Add, after `a.getCmd.AddCommand` (the "Get a Campaign by id" block) — `"encoding/json"` needs
adding to this file's imports:
```go
	a.setCmd.AddCommand(&cobra.Command{
		Use:   "configuration <campaignId> <value>",
		Short: "Replace a Campaign's configuration (an opaque JSON string, not parsed or validated)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PUT", "/campaigns/"+args[0]+"/configuration", map[string]any{"configuration": args[1]})
		},
	})

	a.actionCmd.AddCommand(&cobra.Command{
		Use:   "campaign <campaignId> <jsonPayload>",
		Short: "Request configuration of a Campaign",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(args[1]), &payload); err != nil {
				return fmt.Errorf("invalid JSON payload: %w", err)
			}
			return client.Command("PUT", "/campaigns/"+args[0]+"/configure", payload)
		},
	})
```

- [ ] **Step 17: Build, vet, test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all clean.

- [ ] **Step 18: Commit**

```bash
git add internal/domain/campaign/events/events.go internal/domain/campaign/campaign.go \
  internal/domain/campaign/campaign_test.go internal/command/campaign/service.go \
  api/command/openapi.yaml api/command/gen/server.gen.go internal/httpapi/command/server.go \
  internal/projection/campaign/migrations/0003_campaign_configuration.up.sql \
  internal/projection/campaign/migrations/0003_campaign_configuration.down.sql \
  internal/projection/campaign/projector.go internal/query/campaign/repository.go \
  api/query/openapi.yaml api/query/gen/server.gen.go internal/httpapi/query/server.go \
  internal/cliapp/root.go internal/cliapp/campaign.go
git commit -m "campaign: add configuration field + configure trigger endpoint

Mirrors Character's info/action feature: PUT .../configuration sets
Campaign.configuration directly (SetConfiguration, ConfigurationChanged);
PUT .../configure raises ConfigurationRequested without mutating the
aggregate (Apply has an explicit no-op case) -- the actual effect, if
any, is decided later and asynchronously by timadorus-engine's
CampaignProcessor (added in a following commit), conditionally on this
Campaign's own Ruleset name. configuration is exposed read-only via
GET .../campaigns.

go build/vet/test clean, including new SetConfiguration/
RequestConfiguration domain tests (raises exactly one event each,
RequestConfiguration mutates no field, archived guard on both)."
```

---

### Task 2: `timadorus-engine` — split into `CharacterProcessor` + `CampaignProcessor`, sharing one `RulesetCache`

**Files:**
- Modify: `internal/engine/timadorus/cache.go` (export `RulesetCache`, add `resolve` method, update package doc)
- Modify: `internal/engine/timadorus/cache_test.go`
- Rename+modify: `internal/engine/timadorus/processor.go` → `internal/engine/timadorus/character_processor.go`
- Rename+modify: `internal/engine/timadorus/processor_test.go` → `internal/engine/timadorus/character_processor_test.go`
- Create: `internal/engine/timadorus/campaign_processor.go`
- Create: `internal/engine/timadorus/campaign_processor_test.go`
- Modify: `cmd/timadorus-engine/main.go`

**Interfaces:**
- Consumes: `events.TypeConfigurationRequested`/`events.ConfigurationRequested` (Task 1),
  `campaign.Campaign`/`campaign.ErrArchived`/`campaign.AggregateType` (existing).
- Produces: `timadorus.RulesetCache`, `timadorus.NewRulesetCache() *RulesetCache`,
  `timadorus.NewCharacterProcessor(pool *pgxpool.Pool, cache *RulesetCache) *CharacterProcessor`,
  `timadorus.NewCampaignProcessor(pool *pgxpool.Pool, cache *RulesetCache) *CampaignProcessor`
  (consumed by `cmd/timadorus-engine/main.go`).

- [ ] **Step 1: Export `RulesetCache` and add `resolve` in `internal/engine/timadorus/cache.go`**

Replace the file's contents:
```go
// Package timadorus is timadorus-engine's event processors: internal/projection.Projector
// implementations that react to Character ActionRequested and Campaign ConfigurationRequested
// events, conditionally (based on the relevant Campaign's Ruleset name) appending a timestamp
// to that aggregate's opaque string field. See
// docs/superpowers/specs/2026-08-23-character-action-timadorus-engine-design.md and
// docs/superpowers/specs/2026-08-23-campaign-configuration-timadorus-engine-design.md.
package timadorus

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// errPrefix prefixes every error this package's processors return, so a future rename can't
// silently leave a stale string behind in error messages.
const errPrefix = "timadorus-engine: "

// RulesetCache caches each Campaign's Ruleset name, keyed by campaign id. A Campaign's Ruleset
// reference is set once at creation and never changes (no such command exists on Campaign), so
// once resolved, a lookup never needs repeating. Shared by both CharacterProcessor and
// CampaignProcessor (constructed once in cmd/timadorus-engine/main.go and injected into both) —
// a Character-triggered lookup and a Campaign-triggered lookup for the same campaign id resolve
// to the same cached entry. Guarded by a mutex for defensiveness only: each Processor's Handle
// runs on a single goroutine today (one subject per processor, SubscribersCount: 1 —
// internal/bus.NewSubscriber's doc comment), mirroring how projection.Router itself guards its
// own single-goroutine-today attempts map (internal/projection/router.go).
type RulesetCache struct {
	mu    sync.RWMutex
	names map[uuid.UUID]string
}

func NewRulesetCache() *RulesetCache {
	return &RulesetCache{names: make(map[uuid.UUID]string)}
}

func (c *RulesetCache) get(campaignID uuid.UUID) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	name, ok := c.names[campaignID]
	return name, ok
}

// set is only ever called by resolve after a fully successful lookup — a not-found/error result
// is never cached, so a transient "campaign not projected yet" failure doesn't poison the cache;
// the next redelivery attempt just re-queries.
func (c *RulesetCache) set(campaignID uuid.UUID, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.names[campaignID] = name
}

// resolve returns campaignID's Ruleset name, from cache if present, otherwise via the one
// joined query both processors need — called identically by CharacterProcessor (after an extra
// characters_read_model hop to find the campaign id) and CampaignProcessor (whose own
// env.AggregateID already is the campaign id).
func (c *RulesetCache) resolve(ctx context.Context, tx pgx.Tx, campaignID uuid.UUID) (string, error) {
	if name, ok := c.get(campaignID); ok {
		return name, nil
	}

	var name string
	err := tx.QueryRow(ctx,
		`SELECT r.name FROM campaigns_read_model c
		 JOIN rulesets_read_model r ON r.id = c.ruleset_id
		 WHERE c.id = $1`, campaignID,
	).Scan(&name)
	if err != nil {
		return "", fmt.Errorf(errPrefix+"look up ruleset for campaign %s: %w", campaignID, err)
	}

	c.set(campaignID, name)
	return name, nil
}
```

- [ ] **Step 2: Update `internal/engine/timadorus/cache_test.go`**

```go
package timadorus

import (
	"testing"

	"github.com/google/uuid"
)

func TestRulesetCache(t *testing.T) {
	c := NewRulesetCache()
	campaignID := uuid.New()

	if _, ok := c.get(campaignID); ok {
		t.Fatal("got a hit on an empty cache, want a miss")
	}

	c.set(campaignID, "Timadorus")

	name, ok := c.get(campaignID)
	if !ok {
		t.Fatal("got a miss right after set, want a hit")
	}
	if name != "Timadorus" {
		t.Fatalf("got %q, want %q", name, "Timadorus")
	}

	// A second campaign must not collide with the first.
	other := uuid.New()
	if _, ok := c.get(other); ok {
		t.Fatal("got a hit for an unrelated campaign id, want a miss")
	}
}
```

- [ ] **Step 3: Rename `processor.go` to `character_processor.go` and update it**

```bash
git mv internal/engine/timadorus/processor.go internal/engine/timadorus/character_processor.go
```

Replace its contents:
```go
package timadorus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// CharacterProcessorName is both the durable JetStream consumer name and the checkpoint table
// key (see projection.Projector.Name's doc comment). Reuses the shared projection_checkpoints
// table (internal/projection/checkpoint) — no new migration needed.
const CharacterProcessorName = "timadorus-engine"

// targetRulesetName is matched case-insensitively against each triggering aggregate's Campaign's
// Ruleset name (design spec §2) — the one hardcoded piece of business logic both processors in
// this package share.
const targetRulesetName = "timadorus"

// CharacterProcessor implements projection.Projector unmodified, but — unlike every read-model
// projector — legitimately re-enters the write side (loads and saves a Character aggregate).
// This is why it lives in its own binary (cmd/timadorus-engine) rather than alongside the
// read-only projectors in cmd/projector: importing domain/character/eventsourcing/eventstore
// here would break that binary's documented "never imports domain invariant code" guarantee.
//
// Each action rewrites the entire "actions" array into a new event payload (see
// appendActionTimestamp), so the cost of N actions on one Character is O(N^2) bytes across
// the event log — a known, accepted consequence of reusing SetInfo rather than a new
// mutation path, not a bug, but worth flagging for whoever later sizes this feature for
// heavy use.
//
// Unlike every existing (idempotent, upsert-based) projector, this Processor's effect is not
// naturally idempotent on replay: a checkpoint reset would re-append every historical
// timestamp rather than converge to the same state. Relatedly, a dead-lettered
// ActionRequested that's later replayed after the checkpoint has already advanced past it is
// silently skipped, not reprocessed.
type CharacterProcessor struct {
	characters *eventsourcing.Repository[*character.Character]
	cache      *RulesetCache
}

// NewCharacterProcessor builds its own Registry scoped to just Character — the only aggregate
// type this processor ever loads/saves — mirroring cmd/command-api/main.go's construction
// pattern (registry -> postgres.NewStore(pool, registry) -> eventsourcing.NewRepository(store,
// ...)). cache is shared with CampaignProcessor — see RulesetCache's doc comment.
func NewCharacterProcessor(pool *pgxpool.Pool, cache *RulesetCache) *CharacterProcessor {
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	return &CharacterProcessor{
		characters: eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
			return &character.Character{}
		}),
		cache: cache,
	}
}

func (p *CharacterProcessor) Name() string { return CharacterProcessorName }

func (p *CharacterProcessor) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *CharacterProcessor) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	if env.EventType != events.TypeActionRequested {
		return nil
	}
	var e events.ActionRequested
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf(errPrefix+"unmarshal %s: %w", env.EventType, err)
	}

	var campaignID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT campaign_id FROM characters_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&campaignID); err != nil {
		return fmt.Errorf(errPrefix+"look up campaign for character %s: %w", env.AggregateID, err)
	}

	rulesetName, err := p.cache.resolve(ctx, tx, campaignID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(rulesetName, targetRulesetName) {
		return nil // not our ruleset — no-op, still checkpointed as handled
	}

	return p.appendActionTimestamp(ctx, tx, env, e.OccurredAt)
}

// appendActionTimestamp loads the Character — a plain read against committed state via the
// pool, outside tx (postgres.Store.Load never consults the ambient transaction; only the
// later Append/Save does), so a save further down can still race a concurrent writer. That's
// fine: the save goes through postgres.WithTx and is guarded by the aggregate's optimistic
// concurrency check, so a stale load just fails Handle, which the Router already retries via
// Nack + redelivery, re-Loading the current version on the next attempt. It appends
// occurredAt to info's "actions" list and saves via the already-existing SetInfo — reusing
// the whole InfoChanged/projector/read-model pipeline built for that feature, not a new
// mutation path.
func (p *CharacterProcessor) appendActionTimestamp(ctx context.Context, tx pgx.Tx, env bus.Envelope, occurredAt time.Time) error {
	characterID := env.AggregateID
	// Thread the originating request's correlation id through to the InfoChanged event this
	// appends, so its metadata isn't stamped empty (the Router's own ctx carries none) and the
	// event log keeps its causal link back to the PUT .../action request that triggered it.
	txCtx := postgres.WithTx(observability.WithCorrelationID(ctx, env.CorrelationID()), tx)

	c, err := p.characters.Load(txCtx, characterID)
	if err != nil {
		return fmt.Errorf(errPrefix+"load character %s: %w", characterID, err)
	}

	// Parse into a generic map, touching only "actions" — never discard other top-level keys a
	// human might have set via the already-existing PUT .../info endpoint. If the existing
	// info isn't valid JSON, or isn't a JSON object at all (allowed today: SetInfo accepts any
	// string), start fresh rather than erroring — retrying won't fix malformed content, so
	// erroring here would get this Character permanently stuck instead of self-healing.
	info := map[string]any{}
	if raw := c.Info(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &info) // best-effort; info stays {} on failure
	}
	actions, _ := info["actions"].([]any)
	info["actions"] = append(actions, occurredAt.UTC().Format(time.RFC3339Nano))

	newInfo, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf(errPrefix+"marshal updated info for character %s: %w", characterID, err)
	}

	if err := c.SetInfo(string(newInfo)); err != nil {
		// A Character archived between the PUT .../action request and this engine processing
		// the resulting ActionRequested is a legitimate, expected race (archive is immediate;
		// this engine only catches up later) — not a failure to retry. Retrying can't
		// un-archive the aggregate, so treating it as an error would get this event
		// permanently dead-lettered instead of cleanly no-op'd, same rationale as the
		// malformed-info case above.
		if errors.Is(err, character.ErrArchived) {
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

- [ ] **Step 4: Rename `processor_test.go` to `character_processor_test.go` and update it**

```bash
git mv internal/engine/timadorus/processor_test.go internal/engine/timadorus/character_processor_test.go
```

Within the file, change the one `timadorus.NewProcessor(pool)` call (in `runEngine`) to:
```go
	p := timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache())
```

For symmetry with the new `TestCampaignProcessor_...` names Step 8 adds, rename this file's four
test functions (a straight find-and-replace, no behavior change):
`TestProcessor_MatchingRuleset_AppendsTimestamp` → `TestCharacterProcessor_MatchingRuleset_AppendsTimestamp`,
`TestProcessor_NonMatchingRuleset_NoOp` → `TestCharacterProcessor_NonMatchingRuleset_NoOp`,
`TestProcessor_ArchivedCharacter_NoOp` → `TestCharacterProcessor_ArchivedCharacter_NoOp`,
`TestProcessor_PreservesCorrelationID` → `TestCharacterProcessor_PreservesCorrelationID`. No
other changes needed — every other reference in this file (`character.*`, `events.*`, the helper
functions) is unaffected by the rename.

- [ ] **Step 5: Build the renamed character processor in isolation**

Run: `go build ./internal/engine/... && go vet ./internal/engine/...`
Expected: clean (confirms the rename didn't miss a reference anywhere in this package).

- [ ] **Step 6: Create `internal/engine/timadorus/campaign_processor.go`**

```go
package timadorus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign"
	"github.com/timadorus/platform/internal/domain/campaign/events"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
	"github.com/timadorus/platform/internal/observability"
)

// CampaignProcessorName is both the durable JetStream consumer name and the checkpoint table
// key — distinct from CharacterProcessorName so both processors get their own independent
// checkpoint row in the shared projection_checkpoints table, even though they share a binary
// and a RulesetCache.
const CampaignProcessorName = "timadorus-engine-campaign"

// CampaignProcessor is CharacterProcessor's sibling: same shape, same rationale for living in
// this binary, reacting to Campaign's own ConfigurationRequested instead of Character's
// ActionRequested. Its ruleset lookup is simpler than CharacterProcessor's: a Campaign event's
// own env.AggregateID already is the campaign id, so no characters_read_model hop is needed.
// Not naturally idempotent on replay, for the same reason CharacterProcessor isn't — see that
// type's doc comment.
type CampaignProcessor struct {
	campaigns *eventsourcing.Repository[*campaign.Campaign]
	cache     *RulesetCache
}

// NewCampaignProcessor mirrors NewCharacterProcessor's construction, scoped to Campaign. cache
// is shared with CharacterProcessor — see RulesetCache's doc comment.
func NewCampaignProcessor(pool *pgxpool.Pool, cache *RulesetCache) *CampaignProcessor {
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	return &CampaignProcessor{
		campaigns: eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
			return &campaign.Campaign{}
		}),
		cache: cache,
	}
}

func (p *CampaignProcessor) Name() string { return CampaignProcessorName }

func (p *CampaignProcessor) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *CampaignProcessor) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	if env.EventType != events.TypeConfigurationRequested {
		return nil
	}
	var e events.ConfigurationRequested
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf(errPrefix+"unmarshal %s: %w", env.EventType, err)
	}

	// A Campaign event's own aggregate id already is the campaign id — no extra hop needed
	// (contrast CharacterProcessor.Handle, which first has to look up campaign_id from
	// characters_read_model).
	campaignID := env.AggregateID

	rulesetName, err := p.cache.resolve(ctx, tx, campaignID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(rulesetName, targetRulesetName) {
		return nil // not our ruleset — no-op, still checkpointed as handled
	}

	return p.appendConfigurationTimestamp(ctx, tx, env, e.OccurredAt)
}

// appendConfigurationTimestamp mirrors CharacterProcessor.appendActionTimestamp exactly, with
// two differences: it operates on Campaign/Configuration/SetConfiguration instead of
// Character/Info/SetInfo, and the JSON list key is "configs", not "actions" (deliberately
// different from Character's shape).
func (p *CampaignProcessor) appendConfigurationTimestamp(ctx context.Context, tx pgx.Tx, env bus.Envelope, occurredAt time.Time) error {
	campaignID := env.AggregateID
	txCtx := postgres.WithTx(observability.WithCorrelationID(ctx, env.CorrelationID()), tx)

	c, err := p.campaigns.Load(txCtx, campaignID)
	if err != nil {
		return fmt.Errorf(errPrefix+"load campaign %s: %w", campaignID, err)
	}

	config := map[string]any{}
	if raw := c.Configuration(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &config) // best-effort; config stays {} on failure
	}
	configs, _ := config["configs"].([]any)
	config["configs"] = append(configs, occurredAt.UTC().Format(time.RFC3339Nano))

	newConfig, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf(errPrefix+"marshal updated configuration for campaign %s: %w", campaignID, err)
	}

	if err := c.SetConfiguration(string(newConfig)); err != nil {
		// A Campaign archived between the PUT .../configure request and this engine processing
		// the resulting ConfigurationRequested is a legitimate, expected race — same rationale
		// as CharacterProcessor.appendActionTimestamp's identical guard.
		if errors.Is(err, campaign.ErrArchived) {
			return nil
		}
		return fmt.Errorf(errPrefix+"set configuration for campaign %s: %w", campaignID, err)
	}
	if err := p.campaigns.Save(txCtx, c); err != nil {
		return fmt.Errorf(errPrefix+"save campaign %s: %w", campaignID, err)
	}
	return nil
}
```

- [ ] **Step 7: Build**

Run: `go build ./internal/engine/... && go vet ./internal/engine/...`
Expected: clean.

- [ ] **Step 8: Create `internal/engine/timadorus/campaign_processor_test.go`**

Mirrors `character_processor_test.go`'s structure and helpers, adapted for Campaign — no
Character/Entity involved at all, since `ConfigurationRequested`'s aggregate id is the campaign
id directly.

```go
package timadorus_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign"
	"github.com/timadorus/platform/internal/domain/campaign/events"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
	"github.com/timadorus/platform/internal/projection"
)

func newCampaignTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../../eventstore/postgres/migrations/0001_events.up.sql",
			"../../eventstore/postgres/migrations/0002_outbox.up.sql",
			"../../projection/checkpoint/migrations/0001_projection_checkpoints.up.sql",
			"../../projection/checkpoint/migrations/0002_projection_dead_letters.up.sql",
			"../../projection/campaign/migrations/0001_campaign_read_model.up.sql",
			"../../projection/campaign/migrations/0002_campaign_ruleset_id.up.sql",
			"../../projection/campaign/migrations/0003_campaign_configuration.up.sql",
			"../../projection/ruleset/migrations/0001_ruleset_read_model.up.sql",
		),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func campaignRepo(pool *pgxpool.Pool) *eventsourcing.Repository[*campaign.Campaign] {
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	return eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
}

// createCampaign drives a real Campaign through the real event store, then seeds the
// campaigns_read_model row (which the projector would normally populate) with rulesetID so the
// Processor's ruleset lookup has something to join against.
func createCampaign(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	repo := campaignRepo(pool)

	c, err := campaign.New(uuid.New(), rulesetID, "Test Campaign", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("campaign.New: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save campaign: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, configuration, is_archived, updated_at)
		 VALUES ($1, $2, $3, $4, '', false, now())`,
		c.AggregateID(), c.Name(), c.UniverseID(), rulesetID,
	); err != nil {
		t.Fatalf("seed campaigns_read_model: %v", err)
	}
	return c.AggregateID()
}

func seedRuleset(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID, name string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO rulesets_read_model (id, name, description, reference_urls, is_archived, updated_at)
		 VALUES ($1, $2, '', '{}', false, now())`, rulesetID, name,
	); err != nil {
		t.Fatalf("seed ruleset: %v", err)
	}
}

func archiveCampaign(t *testing.T, pool *pgxpool.Pool, campaignID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	repo := campaignRepo(pool)

	c, err := repo.Load(ctx, campaignID)
	if err != nil {
		t.Fatalf("load campaign: %v", err)
	}
	if err := c.Archive(); err != nil {
		t.Fatalf("archive campaign: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save archived campaign: %v", err)
	}
}

func runCampaignEngine(t *testing.T, pool *pgxpool.Pool) (publish func(env bus.Envelope), wait func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })

	p := timadorus.NewCampaignProcessor(pool, timadorus.NewRulesetCache())
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardCampaignLogger())

	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	subject := bus.Subject(events.AggregateType)
	publish = func(env bus.Envelope) {
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(subject, msg); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	wait = func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("router.Run: %v", err)
		}
	}
	return publish, wait
}

func TestCampaignProcessor_MatchingRuleset_AppendsTimestamp(t *testing.T) {
	pool := newCampaignTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "TIMADORUS") // exact-case mismatch on purpose
	campaignID := createCampaign(t, pool, rulesetID)

	publish, wait := runCampaignEngine(t, pool)

	occurredAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshalCampaign(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: occurredAt}),
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
		Configs []string `json:"configs"`
	}
	if err := json.Unmarshal([]byte(configEvent.Configuration), &decoded); err != nil {
		t.Fatalf("configuration %q is not the expected shape: %v", configEvent.Configuration, err)
	}
	if len(decoded.Configs) != 1 || decoded.Configs[0] != occurredAt.Format(time.RFC3339Nano) {
		t.Fatalf("got configs %v, want [%q]", decoded.Configs, occurredAt.Format(time.RFC3339Nano))
	}

	wait()
}

func TestCampaignProcessor_NonMatchingRuleset_NoOp(t *testing.T) {
	pool := newCampaignTestPool(t)

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
		Payload:       mustMarshalCampaign(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
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

func TestCampaignProcessor_ArchivedCampaign_NoOp(t *testing.T) {
	pool := newCampaignTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)
	archiveCampaign(t, pool, campaignID)

	publish, wait := runCampaignEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshalCampaign(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})

	time.Sleep(500 * time.Millisecond)

	var configChangedCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		campaignID, events.TypeConfigurationChanged,
	).Scan(&configChangedCount); err != nil {
		t.Fatalf("count configuration_changed events: %v", err)
	}
	if configChangedCount != 0 {
		t.Fatalf("got %d campaign.configuration_changed.v1 events, want 0 (campaign is archived)", configChangedCount)
	}

	var deadLetterCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM projection_dead_letters WHERE aggregate_id = $1`,
		campaignID,
	).Scan(&deadLetterCount); err != nil {
		t.Fatalf("count dead letters: %v", err)
	}
	if deadLetterCount != 0 {
		t.Fatalf("got %d dead-lettered events, want 0 (archived Campaign should be a clean no-op)", deadLetterCount)
	}

	wait()
}

func TestCampaignProcessor_PreservesCorrelationID(t *testing.T) {
	pool := newCampaignTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)

	publish, wait := runCampaignEngine(t, pool)

	const correlationID = "test-campaign-correlation-id-12345"
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshalCampaign(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
		Metadata:      mustMarshalCampaign(t, map[string]string{"correlation_id": correlationID, "causation_id": correlationID}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var metadata []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT metadata FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&metadata)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query configuration_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if metadata == nil {
		t.Fatal("timed out waiting for a campaign.configuration_changed.v1 event")
	}

	var decoded struct {
		CorrelationID string `json:"correlation_id"`
	}
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		t.Fatalf("metadata %s is not the expected shape: %v", metadata, err)
	}
	if decoded.CorrelationID != correlationID {
		t.Fatalf("got correlation_id %q, want %q", decoded.CorrelationID, correlationID)
	}

	wait()
}

// TestSharedRulesetCache_ServesBothProcessors proves the design's central claim: one
// RulesetCache instance, populated by a Campaign-triggered lookup, is then used as-is by a
// Character-triggered lookup for the same campaign — without a second query — even after the
// underlying ruleset name changes in Postgres. This exercises both processors together, wired
// exactly as cmd/timadorus-engine/main.go wires them.
func TestSharedRulesetCache_ServesBothProcessors(t *testing.T) {
	pool := newCampaignTestPool(t)
	// Character migrations are needed too since this test also drives a Character-triggered
	// lookup through CharacterProcessor.
	if _, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS characters_read_model (
			id UUID PRIMARY KEY, name TEXT NOT NULL, campaign_id UUID NOT NULL,
			entity_id UUID NOT NULL, player_user_id UUID NOT NULL, info TEXT NOT NULL DEFAULT '',
			is_archived BOOLEAN NOT NULL DEFAULT false, updated_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		t.Fatalf("create characters_read_model: %v", err)
	}

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)

	cache := timadorus.NewRulesetCache()
	campaignProcessor := timadorus.NewCampaignProcessor(pool, cache)
	characterProcessor := timadorus.NewCharacterProcessor(pool, cache)

	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardCampaignLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{campaignProcessor, characterProcessor}) }()

	publish := func(subject string, env bus.Envelope) {
		body := mustMarshalCampaign(t, env)
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(subject, msg); err != nil {
			t.Fatalf("publish to %s: %v", subject, err)
		}
	}

	// First: a Campaign-triggered lookup populates the shared cache with "timadorus".
	publish(bus.Subject(events.AggregateType), bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeConfigurationRequested,
		Payload:   mustMarshalCampaign(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})
	waitForConfigurationChanged(t, pool, campaignID)

	// Mutate the ruleset's name in Postgres directly. If CharacterProcessor re-queried instead
	// of using the shared cache, it would see this new name and (correctly, per its own logic)
	// decide not to act — so a Character-triggered append succeeding after this proves the
	// cache, not a fresh query, served the lookup.
	if _, err := pool.Exec(context.Background(),
		`UPDATE rulesets_read_model SET name = 'ChangedLater' WHERE id = $1`, rulesetID,
	); err != nil {
		t.Fatalf("mutate ruleset name: %v", err)
	}

	characterID := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO characters_read_model (id, name, campaign_id, entity_id, player_user_id, info, is_archived, updated_at)
		 VALUES ($1, 'Elminster', $2, $3, $4, '', false, now())`,
		characterID, campaignID, uuid.New(), uuid.New(),
	); err != nil {
		t.Fatalf("seed characters_read_model: %v", err)
	}

	publish(bus.Subject(characterevents.AggregateType), bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: characterevents.AggregateType, Version: 2,
		EventType: characterevents.TypeActionRequested,
		Payload:   mustMarshalCampaign(t, characterevents.ActionRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
			characterID, characterevents.TypeInfoChanged,
		).Scan(&count); err != nil {
			t.Fatalf("poll info_changed: %v", err)
		}
		if count == 1 {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatal("timed out waiting for character.info_changed.v1 — CharacterProcessor did not use the shared cache (it must have re-queried and seen the changed ruleset name)")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

func waitForConfigurationChanged(t *testing.T, pool *pgxpool.Pool, campaignID uuid.UUID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&count); err != nil {
			t.Fatalf("poll configuration_changed: %v", err)
		}
		if count == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for campaign.configuration_changed.v1")
}

func mustMarshalCampaign(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func discardCampaignLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
```

- [ ] **Step 9: Run all of this package's tests**

Run: `go test ./internal/engine/timadorus/... -v`
Expected: `TestRulesetCache`, all four `TestProcessor_...` (Character) tests, all four
`TestCampaignProcessor_...` tests, and `TestSharedRulesetCache_ServesBothProcessors` all PASS.
Requires Docker (testcontainers).

- [ ] **Step 10: Wire both processors into `cmd/timadorus-engine/main.go`**

Change:
```go
	router := projection.NewRouter(pool, newSubscriber, logger)

	processors := []projection.Projector{
		timadorusengine.NewProcessor(pool),
	}
```
to:
```go
	router := projection.NewRouter(pool, newSubscriber, logger)

	cache := timadorusengine.NewRulesetCache()
	processors := []projection.Projector{
		timadorusengine.NewCharacterProcessor(pool, cache),
		timadorusengine.NewCampaignProcessor(pool, cache),
	}
```

Update the file's top package comment (currently describes only the Character flow):
```go
// timadorus-engine subscribes to the Character and Campaign event streams and reacts to their
// respective trigger events (ActionRequested, ConfigurationRequested) — see
// internal/engine/timadorus for the actual logic. Structurally identical to cmd/projector (same
// Router/checkpoint machinery), but registers two processors sharing one RulesetCache instead
// of the seven read-model projectors, which is why it's a separate binary: unlike every
// projector, it legitimately imports full write-side packages (domain/character,
// domain/campaign, eventsourcing, eventstore/postgres).
```

- [ ] **Step 11: Build, vet, test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all clean.

- [ ] **Step 12: Commit**

```bash
git add internal/engine/timadorus/ cmd/timadorus-engine/main.go
git commit -m "engine: split timadorus-engine into CharacterProcessor + CampaignProcessor

Renames the existing Processor/ProcessorName/NewProcessor to
CharacterProcessor/CharacterProcessorName/NewCharacterProcessor and
adds a sibling CampaignProcessor reacting to Campaign's
ConfigurationRequested -- one Processor per aggregate type, matching
every existing projector convention, rather than one Handle dispatching
across aggregate types.

Both processors share one RulesetCache instance (exported from the
former unexported rulesetCache, its cache-or-query logic now a
resolve method on the type itself), constructed once in
cmd/timadorus-engine/main.go and injected into both -- a
Character-triggered lookup and a Campaign-triggered lookup for the
same campaign id now share one cache entry. CampaignProcessor's own
lookup needs no characters_read_model hop, since a Campaign event's
own aggregate id already is the campaign id.

go build/vet/test clean, including new CampaignProcessor
testcontainers coverage (matching-ruleset append under the \"configs\"
key, non-matching no-op, archived-campaign no-op, correlation-id
propagation) and a cross-processor test proving the shared cache is
actually shared."
```

- [ ] **Step 13: Live end-to-end verification against the dev cluster**

Run `make dev-up` from the repo root (rebuilds/reloads the `timadorus-engine` image with both
processors now registered, redeploys via `helm upgrade` — no new image/Dockerfile/Helm template
needed, same component as before). Confirm via `kubectl logs` that the pod's startup log now
shows `processors=2`.

Using the same "Direct API access" pattern `make dev-up` prints (fetch a token, port-forward
Traefik):

1. **Direct set.** `PUT /api/command/campaigns/{campaignId}/configuration` with
   `{"configuration":"{\"difficulty\":\"hard\"}"}` → `204`; `GET
   /api/query/campaigns/{campaignId}` reflects it.
2. **Matching-ruleset trigger.** Reuse the existing "Bahamut" campaign (Ruleset "Timadorus").
   `PUT /api/command/campaigns/{campaignId}/configure` with `{}` → `204`. Poll `GET
   .../campaigns/{campaignId}` and confirm `configuration` becomes
   `{"configs":["<RFC3339Nano timestamp>"]}`. Call it a second time and confirm two timestamps.
3. **Non-matching-ruleset trigger.** Create a Campaign under a non-"Timadorus" Ruleset (reuse the
   "NotTimadorus" one from the Character feature's own live verification if it still exists, or
   create a fresh one), call `configure`, wait ~5s, confirm `configuration` is unchanged.
4. **Archived guard.** Archive a Campaign, call `configure` against it — expect `409` from the
   command endpoint itself (this is the immediate archived-check in `RequestConfiguration`, a
   different code path from the engine-side archived-race no-op already covered by Task 2's own
   tests).
5. **No regression on Character.** Repeat one Character `action` scenario from the earlier
   feature's own live verification (e.g. `PUT .../characters/{id}/action` on a Character under
   the Bahamut campaign) and confirm `info` still gains a timestamp — proving the
   rename/shared-cache refactor didn't break the existing, already-shipped behavior.

If any step fails, this is real product behavior to debug and fix — not an environment issue to
explain away.

## Final Verification

- `go build ./... && go vet ./... && go test ./...` clean from the repo root.
- `make dev-up` succeeds; `timadorus-engine`'s pod log shows `processors=2`.
- All 5 live-verification scenarios in Task 2 Step 13 pass against the real cluster.
