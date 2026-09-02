# Universe Change Feed — Design

## Context

Today, nothing in the platform tells a client "something changed" beyond the client already
knowing the specific aggregate (and effectively field) to re-check. The SPA works around this
purely for its own session's own actions (`sidebarRefreshSignal`/`bumpSidebarRefresh`,
`pendingEntityId`, the `waitForX` poll-with-timeout helpers) — nothing detects a change made by a
different browser tab, a different user, or the CLI (`timadorusctl`). This spec adds a generic,
polling-based change feed, scoped per Universe, that closes that gap — additively, alongside the
existing session-local mechanisms, not replacing them.

## Decisions

- **Transport: polling, not push.** A new, cheap, pollable read-model endpoint, following this
  platform's own precedent (the outbox relay chose polling over LISTEN/NOTIFY for the same
  "fewer moving parts... a clean, separable future optimization" reasoning). Server-Sent
  Events/WebSocket push is a legitimate future upgrade, deliberately not built here.

- **Scope: per Universe, not global.** `GET /universes/{universeId}/changes` — matches how the
  SPA already scopes everything else to one Universe/Campaign at a time, and avoids every client
  downloading every change on the platform regardless of relevance.

- **The underlying signal already exists — no new write-side event.** Every command that
  succeeds already writes a domain event (with `global_seq`, `aggregate_type`, `aggregate_id`,
  `event_type`, `occurred_at`) before any read model catches up. This feed is a new *read-side*
  projection of that existing stream, not a new kind of event.

- **Five separate, checkpointed `projection.Projector`s, living in `cmd/projector`** (not a new
  binary, not `cmd/timadorus-engine`, not one projector subscribed to five subjects) — one per
  source aggregate type (Universe/Campaign/Entity/Object/Character), each subscribed to exactly
  one subject. This is a correction from an earlier draft of this design, which tried a single
  projector subscribed to all five subjects: `internal/projection/router.go` runs one goroutine
  per subject, but the checkpoint is keyed by `Name()` alone — one shared `last_global_seq` across
  all five goroutines, and `Set` unconditionally overwrites rather than taking a max. A
  higher-`global_seq` event from one subject could commit the checkpoint before a lower-`global_seq`
  event from a *different* subject is processed, making that lower event's own `env.GlobalSeq <=
  lastSeq` check read "already applied" and silently skip it. No existing projector in this
  codebase has ever subscribed to more than one subject, so this hazard had never come up before.
  Five independent single-subject projectors sidesteps it entirely — each gets its own checkpoint,
  matching every other projector's already-proven shape.

- **Resolving each event's owning Universe: plain read-model queries, not an in-memory cache** —
  the one genuinely non-trivial part, because most events don't carry it directly (only `*Created`
  events do). An in-memory cache (shared across the five projectors, mirroring `RulesetCache`) was
  the first instinct, but it doesn't actually work here: with five *independent* durable consumers
  (not five goroutines of one projector), there is no ordering guarantee across them — a
  `CharacterCreated` event could reach the Character projector before the corresponding
  `CampaignCreated` reaches the Campaign projector and populates a shared cache, and this
  codebase's NATS subscriber has no `NakDelay` (already found as a gap by the
  `campaign-creation-default-traits` branch's final review), so the 5-attempt retry budget burns
  in milliseconds — nowhere near enough time to wait out a real replay-catchup window. Instead:
  - `Universe*` events: `universe_id = env.aggregate_id` directly. No query needed.
  - `CampaignCreated`: `universe_id` comes straight from the event payload's own `UniverseID`. No
    query needed.
  - Every *other* Campaign event (`CampaignRenamed`, `GamemasterAdded`/`Removed`,
    `ConfigurationChanged`, `ConfigurationRequested`, `CampaignArchived`): `SELECT universe_id FROM
    campaigns_read_model WHERE id = $1` (the event's own `aggregate_id`).
  - `EntityCreated`/`ObjectCreated`: `universe_id` from the payload directly. Every other
    Entity/Object event: `SELECT universe_id FROM entities_read_model|objects_read_model WHERE id
    = $1`.
  - `CharacterCreated`: `universe_id` resolved via the payload's own `CampaignID`, looked up as
    `SELECT universe_id FROM campaigns_read_model WHERE id = $1`. Every other Character event:
    `SELECT c.universe_id FROM characters_read_model ch JOIN campaigns_read_model c ON c.id =
    ch.campaign_id WHERE ch.id = $1` (one query, not two).
  - **This is exactly `RulesetCache.resolve`'s own already-established pattern** (a plain
    cross-projection read-model join), and it reintroduces that same narrow "immediately after
    creation" race window — already reviewed and accepted in this codebase as a documented
    trade-off in that exact spot. A zero-rows result is treated as a transient error (→ the
    router's normal Nack/retry path), matching how every existing cross-projection lookup in this
    codebase already handles a miss.
  - `User`/`Ruleset` events are **not included** — both are unparented, so "owning Universe"
    doesn't apply to them. A User's rename never appearing in any Universe's feed matches today's
    existing gap; this spec doesn't attempt to solve it.
  - Each of the five projectors is stateless — no mutex, no shared cache, no new failure mode
    beyond the one this codebase already carries elsewhere.

- **Every event on a covered aggregate type produces a change-log row, unconditionally** — no
  per-event-type inclusion/exclusion list to maintain as the domain's event catalog grows. This
  means a pure-trigger event with no visible read-model effect (e.g. `ConfigurationRequested`)
  still produces a row, causing a client to re-fetch and find nothing different — an accepted,
  minor inefficiency traded for not needing to keep a second list in sync with
  `internal/domain/*/events`.

- **`global_seq` is the row's own primary key and the client's polling cursor** — already
  globally ordered by the event store itself, so no separate sequence is needed. A poll is
  `since` (exclusive) a previously-seen `global_seq`; the very first call (on mount) fetches the
  *current* max `global_seq` with no rows, so a client never gets flooded with the Universe's
  entire history on first load.

- **SPA subscription reuses the existing `provide`/`watch(ref)` idiom** — deliberately not a new
  pub-sub/event-emitter abstraction. A new composable owns the fetch-cursor-then-poll loop and
  exposes one `Ref<ChangeEvent | null>` (the latest change), `provide`d once by `WorkspaceView.vue`
  exactly like `sidebarRefreshSignal`/`pendingEntityId` already are. Consumers `watch()` it and
  filter inside their own handler — a detail view checks for its own exact `aggregateId`; a list
  panel checks only `aggregateType` and re-runs its existing query, the same coarse behavior
  `sidebarRefreshSignal` already provides today, now also reachable from another tab or user.

- **Additive, not a replacement.** `sidebarRefreshSignal`/`bumpSidebarRefresh`/`pendingEntityId`
  are untouched — they still give the current session's own actions instant, zero-latency
  feedback. The new feed's only job is catching changes the current session didn't cause itself,
  which nothing today detects at all.

- **Polling starts/stops with the Workspace's own lifecycle** — `onMounted`/`onUnmounted` in
  `WorkspaceView.vue`, matching the existing `AbortController`-on-unmount convention used
  elsewhere in this codebase, at a 5-second interval (slower than the existing 750ms
  eventual-consistency polls, since this is a low-urgency background check, not "wait for my own
  just-submitted action").

## Changes

### `internal/projection/universechanges/migrations/0001_universe_changes_read_model.up.sql`

```sql
CREATE TABLE universe_changes_read_model (
    global_seq     BIGINT PRIMARY KEY,
    universe_id    UUID NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id   UUID NOT NULL,
    event_type     TEXT NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL
);
CREATE INDEX ON universe_changes_read_model (universe_id, global_seq);
```

Wired into **both** `scripts/migrate-up.sh`'s `schema_owners` list **and** `Dockerfile.migrate`'s
matching `COPY` line — the exact pairing this session's own final review just caught being missed
for `ruleset_tables_read_model`. Both must be updated together this time.

### `internal/projection/universechanges/` — five projector files, one shared insert helper

`universe_projector.go`, `campaign_projector.go`, `entity_projector.go`, `object_projector.go`,
`character_projector.go`, each implementing `projection.Projector` against exactly one subject.
Each `Handle` resolves `universe_id` per the rules above (a direct payload/aggregate-id read for
`Universe*` and every `*Created` event, a plain read-model query otherwise), then calls a small
shared `insertChange(ctx, tx, universeID, env)` helper (pure SQL, no business logic) that all five
reuse — the one legitimate, narrow sharing here, not a generic container.

### `cmd/projector/main.go`

Five new lines in the explicit projector-registration list (one per new projector) — this
codebase's own convention: "a new line in `cmd/projector/main.go`'s explicit registration list —
deliberately explicit... for discoverability." Five lines because this feature needed five
independent single-subject projectors, not one line because it's five distinct consumers, not one
logical addition the way every prior projector's own registration line has been.

### `internal/query/universechanges/repository.go`

`Cursor(ctx, universeID) (int64, error)` (current max `global_seq`, or `0` if none) and
`List(ctx, universeID uuid.UUID, since int64, limit int) ([]Change, error)`, ordered by
`global_seq` ascending, capped (matching the existing Entity-search "cap at 20" convention).

### `api/query/openapi.yaml`

```yaml
  /universes/{universeId}/changes/cursor:
    get:
      operationId: getUniverseChangesCursor
      parameters:
        - $ref: "#/components/parameters/UniverseId"
      responses:
        "200":
          description: The current change cursor for this Universe.
          content:
            application/json:
              schema:
                type: object
                required: [globalSeq]
                properties:
                  globalSeq:
                    type: integer
                    format: int64

  /universes/{universeId}/changes:
    get:
      operationId: listUniverseChanges
      summary: List changes to this Universe and everything inside it, after a given cursor.
      parameters:
        - $ref: "#/components/parameters/UniverseId"
        - name: since
          in: query
          required: true
          schema:
            type: integer
            format: int64
      responses:
        "200":
          description: Changes, ordered by globalSeq ascending.
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/UniverseChange"
```

```yaml
    UniverseChange:
      type: object
      required: [globalSeq, aggregateType, aggregateId, eventType, occurredAt]
      properties:
        globalSeq:
          type: integer
          format: int64
        aggregateType:
          type: string
        aggregateId:
          type: string
          format: uuid
        eventType:
          type: string
        occurredAt:
          type: string
          format: date-time
```

### `internal/httpapi/query/server.go`, `cmd/query-api/main.go`

Two new handlers, one new repository wired through, following the exact existing pattern.

### `web/src/composables/useChangeFeed.ts` (new)

Owns the fetch-cursor-then-poll loop; exposes `{ lastChange: Ref<ChangeEvent | null>, start(universeId), stop() }`.

### `web/src/views/WorkspaceView.vue`

Calls `start`/`stop` in `onMounted`/`onUnmounted`; `provide('lastAggregateChange', lastChange)`
alongside the existing `sidebarRefreshSignal`/`pendingEntityId` provides.

### `CharactersPanel.vue`/`EntitiesPanel.vue`/`ObjectsPanel.vue`, and the four detail views

Each additionally `watch`es the injected `lastAggregateChange` ref: list panels re-run their
existing query on any change whose `aggregateType` matches theirs; detail views re-fetch only on
a change whose `aggregateId` matches the one they're currently showing.

## Explicitly Out of Scope

- Server-Sent Events/WebSocket push — a clean future upgrade, not built here.
- Any change feed for `User`/`Ruleset` (unparented, no "owning Universe" to scope by).
- Replacing `sidebarRefreshSignal`/`bumpSidebarRefresh`/`pendingEntityId` — this is additive.
- Any backfill of `universe_changes_read_model` for events that occurred before this projector
  existed — like every other projector, it starts from checkpoint 0 and replays whatever history
  the event store already has, so this is naturally handled, not a gap needing separate work.
- Per-event-type filtering of which events produce a change-log row (see Decisions above).
