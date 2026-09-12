# Realtime Aggregate Updates Design

## Context

The SPA currently learns about backend changes via `useChangeFeed.ts`: every 5 seconds, it polls
`GET /universes/{universeId}/changes?since=<cursor>` (backed by `universe_changes_read_model`, a
dedicated read-model fed by 5 independent NATS-consuming projectors — one each for
Universe/Campaign/Entity/Object/Character — see [[universe-change-feed-design]]). Every component
that cares injects the shared `lastAggregateChange` ref (provided once, in `WorkspaceView.vue`) and
does its own client-side filtering (`aggregateType === 'character' && aggregateId === thisId`).

This already **does** capture "third party" changes and trait-hook side effects (`character.info_
changed.v1` is recorded unconditionally, same as every other event) — the actual problems are:
latency (up to 5s), and three views that never call `useChangeFeed` at all today
(`UniversePickerView`, `CampaignPickerView` — load-once, no refresh mechanism; `UsersAdminView` has
the same gap but is explicitly out of scope, see Decision 8).

This work replaces polling with a push notification, moves per-aggregate filtering from each Vue
component's own `if` check to the backend, and wires every currently-gapped view into the same
mechanism.

## Key Decisions

1. **New service, `cmd/realtime`.** A fifth Go binary (alongside `command-api`/`query-api`/
   `projector`/`timadorus-engine`), matching the existing "one binary per concern" pattern. It
   subscribes to the same 5 NATS subjects the change-feed projectors already consume
   (`events_universe/campaign/entity/object/character`), resolves each event's `universeId` (and,
   for Character events, `campaignId` — new, see Decision 3), and fans matching events out to
   connected browser clients over SSE. It performs no writes and needs no new migration — it reads
   the same read-model tables (`campaigns_read_model`, `entities_read_model`,
   `objects_read_model`, `characters_read_model`) the existing projectors already read.

2. **Transport: Server-Sent Events**, one connection per browser tab, opened once at the app root
   and kept open across navigation. `GET /changes/stream?watch=<filters>` (new `api/realtime`
   OpenAPI spec, its own service — not folded into `api/query`, since it's a different binary with
   a fundamentally different request lifecycle: long-lived and streaming, not request/response).
   Since `EventSource` cannot send messages after connecting, "what am I watching" changes by
   closing and reopening the connection with a new `watch` query value — which is fine, because
   navigation is exactly when the filter needs to change anyway, and reopening is cheap.

3. **Filter clauses — the vocabulary the frontend and backend both speak:**
   ```ts
   type WatchClause =
     | { type: 'universe' }                          // all universes (picker list)
     | { type: 'universe',  aggregateId: string }     // one Universe detail
     | { type: 'campaign',  universeId: string }      // campaign list within a Universe (picker)
     | { type: 'campaign',  aggregateId: string }     // one Campaign detail
     | { type: 'character', campaignId: string }      // character list within a Campaign
     | { type: 'character', aggregateId: string }     // one Character detail
     | { type: 'entity',    universeId: string }      // entity list within a Universe
     | { type: 'entity',    aggregateId: string }      // one Entity detail
     | { type: 'object',    universeId: string }      // object list within a Universe
     | { type: 'object',    aggregateId: string }      // one Object detail
   ```
   `watch` is `encodeURIComponent(JSON.stringify(clauses: WatchClause[]))`. An incoming event
   matches a connection if ANY of its clauses matches: same `type`, and either the clause's
   `aggregateId` equals the event's own id, or its `universeId`/`campaignId` equals the event's
   resolved scope, or (Universe type only) the clause carries no scope at all. This list of ten
   is exhaustive — it's exactly the filtering every existing `lastAggregateChange` watcher already
   does client-side today, plus the two new list-level Universe/Campaign clauses this work adds.
   `campaignId` on a Character clause is the one truly new filtering dimension (Decision 1's
   pipeline doesn't resolve it today — see Decision 4).

4. **Shared resolver package, `internal/aggregateresolve`.** The four non-trivial resolvers
   currently live as unexported methods duplicated one-per-file in
   `internal/projection/universechanges/{campaign,entity,object,character}_projector.go` (Universe
   needs no resolution — its own aggregate id *is* the universe id). This work extracts them into
   a shared, exported package both the existing projectors (write side, called with a `pgx.Tx`) and
   `cmd/realtime` (read side, called with a plain `*pgxpool.Pool` — no ambient transaction) can
   call, via a minimal `Querier` interface (`QueryRow(ctx, sql, args...) pgx.Row`) both types
   already satisfy — mirroring the existing `querier` interface pattern in
   `internal/eventstore/postgres/store.go`. `Character`'s resolver grows a second return value,
   `campaignID`, alongside `universeID` — genuinely new (nothing needed it before this feature),
   requiring both its existing SQL branches (`CharacterCreated` and every other event) to also
   select/return `campaign_id`, which is already a value they touch in their existing joins/lookups
   but never previously returned. `universe_changes_read_model` itself gains no column — the write
   side ignores this new return value; only `cmd/realtime` uses it, resolved live per envelope, not
   persisted.

5. **`cmd/realtime`'s own NATS subscriptions are ephemeral and process-scoped, not one per
   browser connection.** It maintains exactly 5 subscriptions total (matching the 5 aggregate-type
   subjects), for the lifetime of the process — structurally identical to how `cmd/projector`
   already runs 5 independent per-subject consumers in one process (`internal/bus.NewSubscriber`
   already supports one `Subscriber` per `Projector.Name()`, each subscribing to its own subject —
   this is an established, not novel, pattern). The difference: these must be **non-durable**
   (ephemeral) JetStream consumers, not durable ones — `bus.NewSubscriber`'s existing contract
   (`DurablePrefix`/`DurableCalculator`) exists specifically so a *restarted* consumer resumes from
   its last checkpoint, which is the wrong semantic here (a live-notification service restarting
   should pick up from "now," not replay however much history has accumulated since it last ran —
   there's no checkpoint table backing it at all, unlike every projector). This work adds a new
   `bus.NewEphemeralSubscriber(url string, logger watermill.LoggerAdapter) (message.Subscriber,
   error)` — same construction, no durable name — for `cmd/realtime` to use. In-process, an
   in-memory hub (registry of connected clients, each holding its current `[]WatchClause` and a
   buffered outbound channel) receives every resolved event and fans it out to every client whose
   filter matches; a client whose buffer fills (a slow/stuck consumer) is disconnected rather than
   allowed to block the hub, forcing it to reconnect and catch up via Decision 6 below — a standard
   backpressure policy for a fan-out hub.

6. **Catch-up on (re)connect, via the existing polling endpoint — unchanged.** `GET
   /universes/{id}/changes?since=<cursor>` (today's endpoint, today's `universe_changes_read_model`
   projection) stays exactly as it is. Every time the SSE connection (re)opens — whether from an
   application-driven reopen (the filter changed) or the browser's own automatic reconnect-on-error
   (built into `EventSource`) — the frontend first calls this endpoint for whichever single
   `universeId` is currently in scope (in practice there's ever at most one: the Universe the
   SPA is currently working within), then lets the live stream carry updates from there. The
   "watch all universes" picker clause has no cursor to catch up from — reconnecting there just
   means "refetch the universe list once," which is already what that view does on any signal.
   A single, global, monotonically increasing `globalSeq` watermark (events already carry one,
   drawn from one global sequence regardless of aggregate type or universe) makes the two paths
   (catch-up poll, live SSE) safely commutative: whichever arrives, an update is only acted on if
   its `globalSeq` is greater than the last one processed, so a race between "just caught up" and
   "a live event for the same change arrived a moment later" can never double-apply or drop one.

7. **Auth: access token as an SSE query parameter.** `EventSource` cannot set custom headers, so
   the existing `Authorization: Bearer` scheme (`getAccessToken()`/`authMiddleware()` in
   `web/src/api/client.ts`) doesn't reach this endpoint as-is. `cmd/realtime` accepts the same JWT
   via `?access_token=<jwt>` on the SSE URL and validates it exactly like `query-api`'s middleware
   does (reusing `internal/auth`). This is a deliberate, narrow exception to "tokens never go in
   URLs" — the standard, essentially only workaround for authenticating a native `EventSource`
   connection — accepted here because there's no cookie-session scheme anywhere else in this
   codebase to introduce instead, and short-lived access tokens over HTTPS bound this risk.

8. **Scope stays at today's 5 aggregate types.** `User`/`Ruleset` stay excluded from the whole
   change-feed concept, matching the existing design's explicit "unparented" rationale.
   `UsersAdminView`'s own gap is not fixed by this work (per the user's own choice) — it's simply
   left exactly as it is today (load-once, no refresh).

## Component Changes Summary

**Backend (new):**
- `cmd/realtime/main.go` — new binary; config/pool/NATS wiring mirrors `cmd/projector`, HTTP
  serve/shutdown lifecycle mirrors `cmd/query-api` (long-lived streaming responses, not a pure
  background consumer).
- `internal/aggregateresolve/` — new package: `Campaign`, `Entity`, `Object`, `Character` resolver
  functions (the last returning `(universeID, campaignID uuid.UUID, err error)`), each taking the
  shared `Querier` interface.
- `internal/bus/nats.go` — adds `NewEphemeralSubscriber`.
- `internal/projection/universechanges/{campaign,entity,object,character}_projector.go` — their
  own `resolveUniverseID` methods become thin calls into `internal/aggregateresolve`, removing the
  duplicated SQL (Character's call additionally ignores the new `campaignID` return value — the
  write side has no use for it).
- New `api/realtime/openapi.yaml` (own service, own generated server interface, same
  `oapi-codegen` toolchain as `api/command`/`api/query`) — one endpoint,
  `GET /changes/stream?watch=...&access_token=...`.
- Deployment: `Dockerfile.realtime` (copy of `Dockerfile.projector`, binary/port swapped), Helm
  `realtime-deployment.yaml`/`realtime-service.yaml` (mirroring `projector`'s — both `DATABASE_URL`
  and `NATS_URL` env, plus JWT env like `query-api`), `values.yaml` `realtime:` block
  (`containerPort: 8085`, the next free port after `timadorus-engine`'s 8084).

**Frontend:**
- `web/src/composables/useAggregateWatch.ts` (new) — a module-level singleton registry. Exposes
  `watchAggregate(clause: WatchClause)`, callable from any component, returning an unregister
  function (or auto-unregistering via `onScopeDispose`) so a component's interest lasts exactly as
  long as it's mounted. A `computed` union of every currently-registered clause is the single
  source of truth for "what's currently displayed." (A module-level singleton, not `provide`/
  `inject`, because the subscribing components span multiple top-level routes —
  `UniversePickerView`/`CampaignPickerView` sit outside `WorkspaceView`'s subtree entirely, so no
  single provider high enough in the tree exists today; this is the one new state-sharing idiom
  this work introduces into the SPA, alongside the existing `provide`/`inject` convention.)
- `web/src/composables/useChangeFeed.ts` → evolves into the new live-connection composable
  (reusing its existing cursor/catch-up-poll logic verbatim, replacing its `setInterval` loop with
  SSE lifecycle management): opens exactly one `EventSource` for the whole app, reopens it whenever
  the watch registry's combined filter changes, and on every open (for any reason) does the
  Decision 6 catch-up poll for whichever universe is in scope. Still exposes `lastChange` in the
  same shape consumers already read.
- `web/src/App.vue` (or an equivalent app-root component) — owns the one call to this composable,
  replacing `WorkspaceView.vue`'s current instantiation; still `provide('lastAggregateChange', ...)`
  under the same key so every existing injector needs no changes to how it *reads* a change, only
  to how it *registers interest* (see below).
- Every current watcher swaps its own `aggregateType === X && aggregateId === Y` check for a
  `watchAggregate({...})` call at setup time: `CharactersPanel.vue`, `EntitiesPanel.vue`,
  `ObjectsPanel.vue`, `CharacterDetailView.vue`, `EntityDetailView.vue`, `ObjectDetailView.vue`,
  `CampaignOverviewPanel.vue`, `UniverseOverviewPanel.vue` (also drops its own independent
  `useChangeFeed()` instance, now redundant with the single global one). `ConfigurationPanel.vue`
  needs no change (it already piggybacks on its parent's watch).
- `UniversePickerView.vue` and `CampaignPickerView.vue` gain a `watchAggregate` call each, closing
  the two real gaps this work targets.
- `web/src/api/runtimeConfig.ts` — `RuntimeConfig` gains a `realtimeApiBaseUrl` field (this
  endpoint isn't `openapi-fetch`-generated, so it needs its own base URL resolved the same way
  `queryApiBaseUrl` already is).

## Testing Strategy

- **Go unit** (`internal/aggregateresolve`): each of the 4 resolver functions, direct-called
  against a seeded Postgres testcontainer (mirrors existing `universechanges` projector test
  conventions) — including a case proving `Character`'s new `campaignID` return value is correct
  for both its `CharacterCreated` and steady-state branches.
- **Go integration** (`cmd/realtime`): publish a NATS event, assert the SSE endpoint delivers a
  matching frame to a connection whose `watch` filter matches and does NOT deliver it to one whose
  filter doesn't — proves server-side filtering actually filters, not just "delivers everything."
  Also: a slow-consumer connection gets disconnected rather than blocking delivery to others
  (Decision 5's backpressure policy).
- **Go regression** (`internal/projection/universechanges`): existing projector tests continue to
  pass unchanged after their resolvers become thin wrappers over `internal/aggregateresolve` —
  proves the extraction didn't change write-side behavior.
- **Frontend spike, early in implementation**: confirm Playwright's existing `mockBackend.ts`
  route-interception pattern can serve a mocked `text/event-stream` response to a real
  `EventSource` in a real browser context — this is untested territory (no existing SSE usage
  anywhere in this codebase or its test suite) and the whole e2e strategy depends on it working;
  if it doesn't, e2e coverage for this feature needs a different approach, decided before the rest
  of the frontend work is planned in detail.
- **Playwright e2e** (new, once the spike above is validated): a change delivered while a Character
  detail page is open updates it without a page reload (the `character.info_changed.v1` /
  trait-hook scenario the user's original report named); a change to an *unrelated* aggregate does
  not trigger a reload; a dropped/reconnected SSE connection still catches up via the polling
  fallback; `UniversePickerView`/`CampaignPickerView` now refresh on an externally-made change,
  closing the two gaps this work targets.

## Out of Scope

- `User`/`Ruleset` aggregates (Decision 8) — `UsersAdminView`'s own refresh gap is not fixed here.
- Any per-Universe/per-Campaign authorization or visibility model — none exists anywhere in this
  codebase today (confirmed: `ListUniverses`/`ListCampaignsByUniverse` and the existing changes
  endpoints already return everything to any authenticated user, unfiltered), and this work
  doesn't add one; `cmd/realtime`'s SSE endpoint is exactly as permissive as every existing GET
  endpoint is today.
- Retiring the polling endpoint or `useChangeFeed.ts`'s cursor/catch-up logic — it becomes the
  reconnect fallback (Decision 6), not dead code.
- Any change to how `WorkspaceView.vue`/`sidebarRefreshSignal`/`pendingEntityId` work — those stay
  on the existing `provide`/`inject` idiom unchanged.
