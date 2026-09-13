# Realtime Aggregate Updates — Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the SPA's 5-second polling change feed with the `cmd/realtime` SSE push service (already merged to `main`), move per-aggregate filtering to the backend via `WatchClause`, and wire every currently-gapped view (`UniversePickerView`, `CampaignPickerView`) into the same live mechanism.

**Architecture:** A new module-level singleton composable (`useAggregateWatch.ts`) lets any component register a `WatchClause` describing what it's currently displaying. `useChangeFeed.ts` evolves from a `setInterval` poller into an SSE-lifecycle composable: it opens exactly one `EventSource` (owned by `App.vue`, the app root) built from the combined registered clauses, reopens it whenever that combined set changes, and does a one-shot catch-up poll (reusing the existing `/changes?since=` endpoint) every time the connection opens — including the browser's own silent auto-reconnects, via `EventSource.onopen`. Every existing `lastAggregateChange`-watching component swaps its own `aggregateType === X && aggregateId === Y` check for a `watchAggregate({...})` registration; the two gapped picker views gain one each.

**Tech Stack:** Vue 3 `<script setup>`, `EventSource` (native browser API — `cmd/realtime`'s endpoint isn't `openapi-fetch`-generated), Pinia (`useAuthStore` for the access token), Helm/Gateway API for exposing `cmd/realtime` to the browser, Playwright for e2e (a real local `http.Server` for SSE mocking — validated via spike, see Task 12).

**Prerequisite:** `docs/superpowers/specs/2026-09-12-realtime-aggregate-updates-design.md` (approved) and the backend half of this feature (merged to `main` as of commit `92cebc2` — `cmd/realtime`, `internal/realtimehub`, `internal/aggregateresolve`, `GET /changes/stream?watch=<json>&access_token=<jwt>`).

## Global Constraints

- **`WatchClause` vocabulary is exactly the design spec's 10-entry union** (Decision 3) — no additional clause shapes, no partial/optional-field variants beyond what's listed. It is JSON-serialized as `JSON.stringify(clauses)` and sent verbatim as the `watch` query parameter, `encodeURIComponent`-escaped.
- **The `Change`/`AggregateChange` wire shape does not change.** `cmd/realtime` sends `data: <json>\n\n` per event where the JSON is exactly `{ globalSeq: number, aggregateType: string, aggregateId: string, eventType: string, occurredAt: string }` (confirmed directly against `cmd/realtime/main.go`'s `streamHandler` and `internal/realtimehub.Change`) — identical to the existing `AggregateChange` interface in `useChangeFeed.ts`. Do not add fields to it (`campaignId` is resolved and used for backend-side filtering only; it is never sent to the browser).
- **No new unit-test framework.** This project has no Vitest/Jest for the web SPA — verification for every frontend task is `npm run typecheck`, `npm run build`, and (where a task's own scope includes it) `npx playwright test`. Do not introduce one.
- **`useAggregateWatch`'s registry is a deliberate, singular exception to this codebase's `useXxx()`-returns-fresh-per-call-state convention** (matches design spec's own framing: "the one new state-sharing idiom this work introduces... alongside the existing `provide`/`inject` convention"). It exports plain functions/constants directly, not a `useAggregateWatch()` factory — do not wrap it in one.
- **Every reopen of the SSE connection must trigger the Decision-6 catch-up poll**, whether the reopen is application-driven (a `watchAggregate`/`unregister` call changing the combined clause set, or a genuine Universe switch) or the browser's own silent auto-reconnect after a dropped connection. The only correct hook for "the browser silently reconnected" is `EventSource.onopen` — it fires for both cases uniformly. Do not try to detect reconnects via `onerror` or a manual retry loop.
- **A `globalSeq`-based guard is required wherever `lastChange` is written**, because after this change two independent sources (the catch-up poll and the live SSE stream) can both deliver the same event during a race. Never assign `lastChange.value` directly from either source — always go through the one shared "apply if newer" guard (Task 3 names it `applyChange`).
- **Do not touch `WorkspaceView.vue`'s `sidebarRefreshSignal`/`pendingEntityId`/`bumpSidebarRefresh` mechanisms.** They are a separate, unrelated mechanism (explicit user-action-driven refresh signals) and are explicitly out of scope (design spec, "Out of Scope").
- **`User`/`Ruleset` aggregates and `UsersAdminView` stay untouched** (design spec Decision 8) — no `WatchClause` variant exists for either, and none should be added.
- **Follow existing file conventions exactly**: this codebase's Vue components consistently use `computed()` (not a plain `const`) for any value derived from `route.params`, because `vue-router` reuses a component instance across param-only navigations on the same route record — every task below that touches a `route.params`-derived value must preserve this pattern where it already exists and use it where it's newly needed.

---

### Task 1: Expose `cmd/realtime` to the browser (Gateway routes + `web.config.realtimeApiBaseUrl` plumbing)

`cmd/realtime` has a Kubernetes Service (`realtime-service.yaml`, merged) but no Gateway `HTTPRoute` — nothing lets a browser reach it today, in either `make dev-up` or a real deployment. This task closes that gap end-to-end: Helm route templates, values wiring, the web ConfigMap, the SPA's `RuntimeConfig` type, the dev-mode `config.json` default, and the two Go call sites (`test/e2e/internal/platform.go`, `test/e2e/cmd/devcluster/up.go`) that already wire the equivalent `commandApiBaseUrl`/`queryApiBaseUrl` values.

**Files:**
- Create: `deploy/helm/timadorus-platform/templates/realtime-httproute.yaml`
- Create: `deploy/helm/timadorus-platform/templates/realtime-httproute-path.yaml`
- Modify: `deploy/helm/timadorus-platform/values.yaml`
- Modify: `deploy/helm/timadorus-platform/templates/web-configmap.yaml`
- Modify: `web/src/api/runtimeConfig.ts`
- Modify: `web/public/config.json`
- Modify: `test/e2e/internal/platform.go`
- Modify: `test/e2e/cmd/devcluster/up.go`

**Interfaces:**
- Produces: `RuntimeConfig.realtimeApiBaseUrl: string` — Task 3's `useChangeFeed.ts` reads this via `getRuntimeConfig()` (this task also adds that synchronous accessor).
- Produces: `export function getRuntimeConfig(): RuntimeConfig` in `runtimeConfig.ts` — throws if called before `loadRuntimeConfig()` has resolved (mirrors `getQueryClient()`'s own "not initialized" guard in `web/src/api/client.ts`), for composables that run after boot.

- [ ] **Step 1: Read the real files first**

  Read `deploy/helm/timadorus-platform/templates/query-api-httproute.yaml`, `query-api-httproute-path.yaml`, and `deploy/helm/timadorus-platform/templates/_helpers.tpl` before writing the new templates below — confirm `timadorus-platform.gatewayParentRefs`/`.fullname`/`.labels` are exactly as shown here; if they differ, match what's actually in the file, not this plan's transcription.

- [ ] **Step 2: Create the hostname-routed HTTPRoute**

  `deploy/helm/timadorus-platform/templates/realtime-httproute.yaml`:

  ```yaml
  apiVersion: gateway.networking.k8s.io/v1
  kind: HTTPRoute
  metadata:
    name: {{ include "timadorus-platform.fullname" . }}-realtime
    labels:
      {{- include "timadorus-platform.labels" . | nindent 4 }}
  spec:
    {{- include "timadorus-platform.gatewayParentRefs" . | nindent 2 }}
    hostnames:
      - {{ required "realtime.route.hostname is required" .Values.realtime.route.hostname | quote }}
    rules:
      - matches:
          - path:
              type: PathPrefix
              value: /
        backendRefs:
          - name: {{ include "timadorus-platform.fullname" . }}-realtime
            port: {{ .Values.realtime.containerPort }}
  ```

- [ ] **Step 3: Create the path-routed HTTPRoute (single shared hostname, for `make dev-up`)**

  `deploy/helm/timadorus-platform/templates/realtime-httproute-path.yaml`:

  ```yaml
  {{- if .Values.gateway.pathRouting.hostname }}
  apiVersion: gateway.networking.k8s.io/v1
  kind: HTTPRoute
  metadata:
    name: {{ include "timadorus-platform.fullname" . }}-realtime-path
    labels:
      {{- include "timadorus-platform.labels" . | nindent 4 }}
  spec:
    {{- include "timadorus-platform.gatewayParentRefs" . | nindent 2 }}
    hostnames:
      - {{ .Values.gateway.pathRouting.hostname | quote }}
    rules:
      - matches:
          - path:
              type: PathPrefix
              value: /api/realtime
        filters:
          - type: URLRewrite
            urlRewrite:
              path:
                type: ReplacePrefixMatch
                replacePrefixMatch: /
        backendRefs:
          - name: {{ include "timadorus-platform.fullname" . }}-realtime
            port: {{ .Values.realtime.containerPort }}
  {{- end }}
  ```

- [ ] **Step 4: Add `realtime.route.hostname` and `web.config.realtimeApiBaseUrl` to `values.yaml`**

  In `deploy/helm/timadorus-platform/values.yaml`, under the existing `realtime:` block (it currently ends after its `resources:` block, right before `migration:`), add a `route:` key matching `commandApi`/`queryApi`'s own shape exactly:

  ```yaml
  realtime:
    image:
      repository: timadorus/realtime
      tag: ""
      pullPolicy: IfNotPresent
    replicas: 1
    containerPort: 8085
    resources:
      requests:
        cpu: 100m
        memory: 128Mi
      limits:
        cpu: 500m
        memory: 256Mi
    route:
      hostname: ""
  ```

  And under the existing `web.config:` block, add `realtimeApiBaseUrl` alongside `commandApiBaseUrl`/`queryApiBaseUrl`:

  ```yaml
    config:
      commandApiBaseUrl: ""
      queryApiBaseUrl: ""
      realtimeApiBaseUrl: ""
      oidc:
  ```

- [ ] **Step 5: Wire `realtimeApiBaseUrl` into the web ConfigMap**

  In `deploy/helm/timadorus-platform/templates/web-configmap.yaml`, add a line for it alongside `queryApiBaseUrl`:

  ```yaml
      "commandApiBaseUrl": {{ required "web.config.commandApiBaseUrl is required" .Values.web.config.commandApiBaseUrl | quote }},
      "queryApiBaseUrl": {{ required "web.config.queryApiBaseUrl is required" .Values.web.config.queryApiBaseUrl | quote }},
      "realtimeApiBaseUrl": {{ required "web.config.realtimeApiBaseUrl is required" .Values.web.config.realtimeApiBaseUrl | quote }},
  ```

- [ ] **Step 6: Verify the Helm chart still renders**

  ```bash
  helm dependency update deploy/helm/timadorus-platform
  helm template timadorus-e2e deploy/helm/timadorus-platform \
    --set postgres.existingSecret=x --set jwt.mode=hmac --set jwt.hmac.existingSecret=x \
    --set gateway.gatewayClassName=x \
    --set commandApi.route.hostname=command-api.platform.test \
    --set queryApi.route.hostname=query-api.platform.test \
    --set realtime.route.hostname=realtime.platform.test \
    --set web.route.hostname=web.platform.test \
    --set web.config.commandApiBaseUrl=x --set web.config.queryApiBaseUrl=x \
    --set web.config.realtimeApiBaseUrl=x \
    --set web.config.oidc.authority=x --set web.config.oidc.clientId=x \
    --set web.config.oidc.redirectUri=x --set web.config.oidc.postLogoutRedirectUri=x \
    > /tmp/rendered.yaml
  grep -A3 "kind: HTTPRoute" /tmp/rendered.yaml | grep -c realtime
  ```

  Expect: no error, and the grep finds the new `realtime` HTTPRoute rendered. Also re-run with `--set gateway.pathRouting.hostname=localhost` added and confirm the `-realtime-path` route additionally renders with `/api/realtime` in its match.

- [ ] **Step 7: Update `RuntimeConfig` and add the synchronous accessor**

  In `web/src/api/runtimeConfig.ts`:

  ```ts
  export interface RuntimeConfig {
    commandApiBaseUrl: string
    queryApiBaseUrl: string
    realtimeApiBaseUrl: string
    oidc: {
      authority: string
      clientId: string
      redirectUri: string
      postLogoutRedirectUri: string
    }
  }

  let cached: RuntimeConfig | null = null

  export async function loadRuntimeConfig(): Promise<RuntimeConfig> {
    if (cached) return cached
    const res = await fetch('/config.json', { cache: 'no-store' })
    if (!res.ok) {
      throw new Error(`failed to load /config.json: ${res.status}`)
    }
    cached = (await res.json()) as RuntimeConfig
    return cached
  }

  // getRuntimeConfig is a synchronous accessor for code that runs after boot (every composable
  // and component — main.ts always awaits loadRuntimeConfig() before mounting the app). Mirrors
  // api/client.ts's getQueryClient()/getCommandClient() "not initialized" guard.
  export function getRuntimeConfig(): RuntimeConfig {
    if (!cached) throw new Error('runtime config not loaded — call loadRuntimeConfig() first')
    return cached
  }
  ```

- [ ] **Step 8: Update the dev-mode config default**

  `web/public/config.json`:

  ```json
  {
    "commandApiBaseUrl": "http://localhost:8081",
    "queryApiBaseUrl": "http://localhost:8082",
    "realtimeApiBaseUrl": "http://localhost:8085",
    "oidc": {
      "authority": "http://localhost:8080/realms/timadorus",
      "clientId": "timadorus-web",
      "redirectUri": "http://localhost:5173/login",
      "postLogoutRedirectUri": "http://localhost:5173/"
    }
  }
  ```

- [ ] **Step 9: Wire the Go e2e installer**

  In `test/e2e/internal/platform.go`:

  Add a hostname constant alongside the existing three:
  ```go
  	commandAPIHostname = "command-api.platform.test"
  	queryAPIHostname   = "query-api.platform.test"
  	realtimeHostname   = "realtime.platform.test"
  	webHostname        = "web.platform.test"
  ```

  Add a field to `PlatformInstallInputs`, next to `WebQueryAPIBaseURL`:
  ```go
  	WebCommandAPIBaseURL string // web.config.commandApiBaseUrl when OIDCAuthority is set
  	WebQueryAPIBaseURL   string // web.config.queryApiBaseUrl when OIDCAuthority is set
  	WebRealtimeAPIBaseURL string // web.config.realtimeApiBaseUrl when OIDCAuthority is set
  ```

  In `InstallPlatform`, add the hostname `--set` next to the other two route hostnames (this one is NOT conditional — `realtime.route.hostname` has no other default, exactly like `commandApi`/`queryApi`'s):
  ```go
  		"--set", "commandApi.route.hostname=" + commandAPIHostname,
  		"--set", "queryApi.route.hostname=" + queryAPIHostname,
  		"--set", "realtime.route.hostname=" + realtimeHostname,
  		"--set", "web.route.hostname=" + webHostname,
  ```

  And extend the placeholder/OIDC-conditional block:
  ```go
  	webCommandAPIBaseURL, webQueryAPIBaseURL := "http://placeholder.e2e.test", "http://placeholder.e2e.test"
  	webRealtimeAPIBaseURL := "http://placeholder.e2e.test"
  	oidcAuthority, oidcClientID := "http://placeholder.e2e.test", "e2e-placeholder"
  	oidcRedirectURI, oidcPostLogoutURI := "http://placeholder.e2e.test/login", "http://placeholder.e2e.test/"
  	if in.OIDCAuthority != "" {
  		webCommandAPIBaseURL = in.WebCommandAPIBaseURL
  		webQueryAPIBaseURL = in.WebQueryAPIBaseURL
  		webRealtimeAPIBaseURL = in.WebRealtimeAPIBaseURL
  		oidcAuthority = in.OIDCAuthority
  		oidcClientID = in.OIDCClientID
  		oidcRedirectURI = in.OIDCRedirectURI
  		oidcPostLogoutURI = in.OIDCPostLogoutURI
  	}
  	args = append(args,
  		"--set", "web.config.commandApiBaseUrl="+webCommandAPIBaseURL,
  		"--set", "web.config.queryApiBaseUrl="+webQueryAPIBaseURL,
  		"--set", "web.config.realtimeApiBaseUrl="+webRealtimeAPIBaseURL,
  		"--set", "web.config.oidc.authority="+oidcAuthority,
  		"--set", "web.config.oidc.clientId="+oidcClientID,
  		"--set", "web.config.oidc.redirectUri="+oidcRedirectURI,
  		"--set", "web.config.oidc.postLogoutRedirectUri="+oidcPostLogoutURI,
  	)
  ```

- [ ] **Step 10: Wire `devcluster up.go`**

  In `test/e2e/cmd/devcluster/up.go`, add one line next to `WebQueryAPIBaseURL` in the `InstallPlatform` call:
  ```go
  		WebCommandAPIBaseURL: fmt.Sprintf("http://localhost:%d/api/command", devGatewayPort),
  		WebQueryAPIBaseURL:   fmt.Sprintf("http://localhost:%d/api/query", devGatewayPort),
  		WebRealtimeAPIBaseURL: fmt.Sprintf("http://localhost:%d/api/realtime", devGatewayPort),
  ```

- [ ] **Step 11: Build and verify**

  ```bash
  cd test/e2e && go build ./... && go vet ./...
  cd ../../web && npm run typecheck
  ```

  Expect both clean.

- [ ] **Step 12: Commit**

  ```bash
  git add deploy/helm/timadorus-platform test/e2e/internal/platform.go test/e2e/cmd/devcluster/up.go web/src/api/runtimeConfig.ts web/public/config.json
  git commit -m "feat(realtime): expose cmd/realtime to the browser via Gateway routes + config plumbing"
  ```

---

### Task 2: `useAggregateWatch.ts` — the clause registry

**Files:**
- Create: `web/src/composables/useAggregateWatch.ts`

**Interfaces:**
- Produces: `export type WatchClause` (the exact 10-entry union from the design spec).
- Produces: `export function watchAggregate(clause: WatchClause): () => void` — registers, auto-unregisters via `onScopeDispose` when called inside an active effect scope (every real call site is a component's `<script setup>`), and also returns the unregister function for symmetry/explicit cleanup.
- Produces: `export const watchedClauses: ComputedRef<WatchClause[]>` — the live union of every currently-registered clause. Task 3 consumes this directly.

- [ ] **Step 1: Write the file**

  `web/src/composables/useAggregateWatch.ts`:

  ```ts
  import { computed, getCurrentScope, onScopeDispose, reactive, type ComputedRef } from 'vue'

  // WatchClause is the exact vocabulary cmd/realtime's GET /changes/stream?watch=... speaks
  // (design spec Decision 3) — this list is exhaustive by design, not partial: it's exactly the
  // filtering every existing lastAggregateChange watcher already did client-side, plus the two
  // new list-level Universe/Campaign clauses this work adds (UniversePickerView/CampaignPickerView).
  export type WatchClause =
    | { type: 'universe' }
    | { type: 'universe'; aggregateId: string }
    | { type: 'campaign'; universeId: string }
    | { type: 'campaign'; aggregateId: string }
    | { type: 'character'; campaignId: string }
    | { type: 'character'; aggregateId: string }
    | { type: 'entity'; universeId: string }
    | { type: 'entity'; aggregateId: string }
    | { type: 'object'; universeId: string }
    | { type: 'object'; aggregateId: string }

  // registry is a module-level singleton — deliberately not per-call state like every other
  // useXxx() composable in this codebase (see this plan's Global Constraints). Components that
  // subscribe span multiple top-level routes (UniversePickerView/CampaignPickerView sit outside
  // WorkspaceView's subtree entirely), so no single provide()/inject() ancestor high enough in
  // the tree exists to hold this instead. reactive(Map), not a plain Map + manual version
  // counter — Vue 3's reactive() fully tracks Map mutations (set/delete), so watchedClauses
  // below recomputes correctly with no extra bookkeeping.
  const registry = reactive(new Map<number, WatchClause>())
  let nextId = 0

  // watchAggregate registers interest in one WatchClause for as long as the calling component
  // stays mounted. Duplicate/overlapping clauses across components are not deduplicated —
  // cmd/realtime's Hub.Broadcast evaluates each clause independently and stops at the first
  // match, so redundant identical clauses cost a little iteration, never incorrect behavior;
  // deduping here would add complexity for no observable benefit (YAGNI).
  export function watchAggregate(clause: WatchClause): () => void {
    const id = nextId++
    registry.set(id, clause)
    const unregister = () => {
      registry.delete(id)
    }
    // Every real call site is inside a component's <script setup> (an active effect scope).
    // getCurrentScope() guards this composable for a caller outside one, where auto-cleanup
    // can't apply and the caller becomes responsible for calling the returned function itself.
    if (getCurrentScope()) onScopeDispose(unregister)
    return unregister
  }

  // watchedClauses is the single source of truth for "what is the SPA currently displaying,
  // across every mounted component" — useChangeFeed.ts (Task 3) watches this directly to decide
  // when to reopen its EventSource.
  export const watchedClauses: ComputedRef<WatchClause[]> = computed(() => Array.from(registry.values()))
  ```

- [ ] **Step 2: Typecheck**

  ```bash
  cd web && npm run typecheck
  ```

  Expect clean (this file has no consumers yet, so this only checks the file compiles in isolation).

- [ ] **Step 3: Commit**

  ```bash
  git add web/src/composables/useAggregateWatch.ts
  git commit -m "feat(web): add useAggregateWatch, the WatchClause registry"
  ```

---

### Task 3: Evolve `useChangeFeed.ts` into the SSE-lifecycle composable

This is the most detail-sensitive task in this plan — read all of it before starting, and do not deviate from the exact control flow below (each piece exists to satisfy one of the Global Constraints: the `applyChange` dedup guard, the `onopen`-driven catch-up, and the `start()` vs. `watch(watchedClauses)` split are all load-bearing, not stylistic choices).

**Files:**
- Modify: `web/src/composables/useChangeFeed.ts`

**Interfaces:**
- Consumes: `watchedClauses` from `web/src/composables/useAggregateWatch.ts` (Task 2).
- Consumes: `useAuthStore().accessToken` from `web/src/stores/auth.ts` (existing — a Pinia getter, `string | null`).
- Consumes: `getRuntimeConfig()` from `web/src/api/runtimeConfig.ts` (Task 1).
- Produces: `export interface AggregateChange` — **unchanged**, every existing consumer's `inject<Ref<AggregateChange | null>>('lastAggregateChange')` continues to work with no changes.
- Produces: `export function useChangeFeed()` returning `{ lastChange, start, stop }` — **same shape** as today. `start(universeId: string)` keeps the same signature; its behavior changes (see below).

- [ ] **Step 1: Replace the file**

  `web/src/composables/useChangeFeed.ts`:

  ```ts
  import { ref } from 'vue'
  import { watch as vueWatch } from 'vue'
  import { getQueryClient } from '@/api/client'
  import { useAuthStore } from '@/stores/auth'
  import { getRuntimeConfig } from '@/api/runtimeConfig'
  import { watchedClauses } from './useAggregateWatch'

  export interface AggregateChange {
    globalSeq: number
    aggregateType: string
    aggregateId: string
    eventType: string
    occurredAt: string
  }

  // useChangeFeed owns the app's single live connection to cmd/realtime (design spec Decision
  // 2/5/6). Only App.vue calls this — every other former caller (WorkspaceView.vue,
  // UniverseOverviewPanel.vue) now reads the same shared change via inject('lastAggregateChange')
  // instead, exactly like every other watcher already does.
  export function useChangeFeed() {
    const lastChange = ref<AggregateChange | null>(null)
    let cursor = 0
    let currentUniverseId = ''
    let eventSource: EventSource | null = null
    // epoch invalidates an in-flight catch-up poll (start() or the onopen handler) that a newer
    // call has already superseded — same pattern as the original polling implementation's own
    // epoch guard, kept for the same reason: a slow response for an abandoned Universe/connection
    // must never land after something newer has already taken over.
    let epoch = 0

    // applyChange is the ONLY place lastChange/cursor are written, from either source (the
    // catch-up poll below, or the live SSE onmessage handler) — see this plan's Global
    // Constraints. Once this feature has two independent delivery paths for the same event, a
    // race between them ("just caught up" vs. "a live event for the same change arrives a moment
    // later") is possible; globalSeq is a single global, monotonically increasing sequence
    // (regardless of aggregate type or universe), so "newer than the last one applied" is always
    // well-defined and safe to compare across both paths.
    function applyChange(change: AggregateChange) {
      if (change.globalSeq <= cursor) return
      cursor = change.globalSeq
      lastChange.value = change
    }

    // catchUp fetches everything since `cursor` for `currentUniverseId`, once (not on an
    // interval) — called every time the SSE connection opens, by openStream()'s onopen handler
    // below. Reuses the original poll() implementation's request/response handling verbatim.
    let inFlight = false
    async function catchUp() {
      if (inFlight || !currentUniverseId) return
      inFlight = true
      const myEpoch = epoch
      try {
        const { data, error } = await getQueryClient().GET('/universes/{universeId}/changes', {
          params: { path: { universeId: currentUniverseId }, query: { since: cursor } },
        })
        if (myEpoch !== epoch) return
        if (!error && data) {
          for (const change of data as AggregateChange[]) {
            applyChange(change)
          }
        }
      } catch (err) {
        // fetch rejects (throws) on a genuine network failure (offline, DNS, connection reset)
        // rather than returning { error } — openapi-fetch only normalizes non-2xx HTTP responses.
        // Log and let the next reconnect retry rather than propagating an unhandled rejection.
        console.error('useChangeFeed: catch-up poll failed', err)
      } finally {
        inFlight = false
      }
    }

    // openStream (re)opens the EventSource against the CURRENT watchedClauses.value and the
    // CURRENT access token, closing any previous connection first. Every open — whether from
    // start() (a Universe switch), the watchedClauses watcher below (a filter change), or the
    // browser's own silent auto-reconnect on an already-open EventSource object — fires onopen,
    // which is the one correct hook for Decision 6's "every time the connection (re)opens, first
    // catch up" rule: it's the only event that fires uniformly for both cases.
    function openStream() {
      eventSource?.close()
      eventSource = null
      const token = useAuthStore().accessToken
      if (!token) return // not authenticated yet; the next start()/clause change retries
      const cfg = getRuntimeConfig()
      const watchParam = encodeURIComponent(JSON.stringify(watchedClauses.value))
      const url = `${cfg.realtimeApiBaseUrl}/changes/stream?watch=${watchParam}&access_token=${encodeURIComponent(token)}`
      const es = new EventSource(url)
      es.onopen = () => {
        void catchUp()
      }
      es.onmessage = (e) => {
        applyChange(JSON.parse(e.data) as AggregateChange)
      }
      eventSource = es
    }

    // Reopen whenever the set of registered clauses changes (a component mounted/unmounted, or
    // its own clause's scope changed) — does NOT touch `cursor`, so the catch-up this triggers
    // (via openStream's onopen) resumes from exactly where the feed already was, per Decision 6.
    vueWatch(watchedClauses, () => {
      if (eventSource) openStream()
    })

    // start establishes (or switches) which Universe's catch-up-poll cursor is in scope — called
    // by App.vue whenever route.params.universeId changes. Only a genuine change re-baselines the
    // cursor; calling start() again with the same id is a no-op (a filter-only change is handled
    // by the watcher above, not by re-running this).
    async function start(universeId: string) {
      if (universeId === currentUniverseId) {
        if (!eventSource) openStream() // first-ever call for this Universe with no connection yet
        return
      }
      currentUniverseId = universeId
      cursor = 0
      const myEpoch = ++epoch
      try {
        const { data, error } = await getQueryClient().GET('/universes/{universeId}/changes/cursor', {
          params: { path: { universeId } },
        })
        if (myEpoch !== epoch) return // a newer start() call has already superseded this one
        if (error || !data) return // leave cursor at 0; a later start() (e.g. a route change) retries
        cursor = data.globalSeq
        openStream()
      } catch (err) {
        console.error('useChangeFeed: start failed', err)
      }
    }

    // stop closes the live connection entirely. Not called by App.vue in normal operation (the
    // app root never unmounts) — kept for symmetry with start() and for any future caller that
    // does need a clean teardown (e.g. a future test harness).
    function stop() {
      epoch++
      eventSource?.close()
      eventSource = null
      currentUniverseId = ''
      cursor = 0
    }

    return { lastChange, start, stop }
  }
  ```

  Note the two `watch` names in play: Vue's own `watch()` function (imported here as `vueWatch` to avoid any confusion with this codebase's `WatchClause` type in surrounding files that import both) and the unrelated concept name. If a reviewer prefers the plain `watch` import name (no alias) since this file doesn't import `WatchClause` itself, that's an acceptable simplification — the alias exists only for reading clarity in this plan.

- [ ] **Step 2: Typecheck**

  ```bash
  cd web && npm run typecheck
  ```

  Expect clean. This file has no consumers changed yet (Tasks 4-11 do that), so this only checks the file compiles and its exported shape (`AggregateChange`, `{ lastChange, start, stop }`) still matches what every existing `inject`/destructure call site expects.

- [ ] **Step 3: Commit**

  ```bash
  git add web/src/composables/useChangeFeed.ts
  git commit -m "feat(web): evolve useChangeFeed into an SSE-lifecycle composable"
  ```

---

### Task 4: Wire `App.vue` as the single owner of the live connection

**Files:**
- Modify: `web/src/App.vue`

**Interfaces:**
- Consumes: `useChangeFeed()` from Task 3.
- Produces: `provide('lastAggregateChange', lastAggregateChange)` — same injection key every existing consumer already reads.

- [ ] **Step 1: Replace the file**

  `web/src/App.vue`:

  ```vue
  <script setup lang="ts">
  import { computed, onUnmounted, provide, watch } from 'vue'
  import { useRoute } from 'vue-router'
  import { useChangeFeed } from '@/composables/useChangeFeed'

  const route = useRoute()
  // computed, not a plain const: App.vue never unmounts, so this must stay reactive across
  // every navigation for the app's entire lifetime, not just param-only ones — the app-root
  // equivalent of every view's own identical comment about vue-router instance reuse.
  const universeId = computed(() => (route.params.universeId as string | undefined) ?? '')

  const { lastChange: lastAggregateChange, start: startChangeFeed, stop: stopChangeFeed } = useChangeFeed()
  provide('lastAggregateChange', lastAggregateChange)

  watch(
    universeId,
    (id) => {
      // An empty id (e.g. the bare "/" Universe-picker route) means there's no single Universe
      // in scope for the catch-up poll — per design spec Decision 6, the live SSE connection
      // itself stays open regardless (a bare {type:'universe'} clause from UniversePickerView
      // still needs it), so there is deliberately no "stop the feed" branch here.
      if (id) startChangeFeed(id)
    },
    { immediate: true },
  )

  onUnmounted(stopChangeFeed)
  </script>

  <template>
    <router-view />
  </template>
  ```

- [ ] **Step 2: Typecheck**

  ```bash
  cd web && npm run typecheck
  ```

- [ ] **Step 3: Commit**

  ```bash
  git add web/src/App.vue
  git commit -m "feat(web): App.vue owns the single live change-feed connection"
  ```

---

### Task 5: `WorkspaceView.vue` — drop its now-redundant change-feed instantiation

**Files:**
- Modify: `web/src/views/WorkspaceView.vue`

- [ ] **Step 1: Remove the `useChangeFeed` import, instantiation, and its two `onMounted`/`watch` lifecycle lines**

  In `web/src/views/WorkspaceView.vue`, remove:
  ```ts
  import { useChangeFeed } from '@/composables/useChangeFeed'
  ```
  ```ts
  const { lastChange: lastAggregateChange, start: startChangeFeed, stop: stopChangeFeed } = useChangeFeed()
  provide('lastAggregateChange', lastAggregateChange)
  ```
  ```ts
  onMounted(() => startChangeFeed(universeId.value))
  watch(universeId, startChangeFeed)
  ```
  and remove `stopChangeFeed()` from the existing `onUnmounted(() => { stopChangeFeed(); loadController?.abort() })`, leaving:
  ```ts
  onUnmounted(() => {
    loadController?.abort()
  })
  ```

  Everything else in this file (`sidebarRefreshSignal`, `pendingEntityId`, `load()`, the template) is untouched — `provide('lastAggregateChange', ...)` now happens once in `App.vue` (Task 4), and `WorkspaceView.vue` sits underneath it in the component tree, so every descendant's existing `inject('lastAggregateChange')` call keeps resolving to the same (now app-root-owned) ref with no changes on the reading side.

- [ ] **Step 2: Typecheck**

  ```bash
  cd web && npm run typecheck
  ```

- [ ] **Step 3: Commit**

  ```bash
  git add web/src/views/WorkspaceView.vue
  git commit -m "refactor(web): WorkspaceView no longer owns a change-feed instance"
  ```

---

### Task 6: `CharactersPanel.vue` — campaign-scoped `watchAggregate`

This is the one panel whose current filter is looser than its own scope (today's `change?.aggregateType === 'character'` check has no `campaignId`/`aggregateId` narrowing at all — it refreshes on ANY Character change anywhere in the current Universe, across every Campaign). `campaignId` on a Character clause is the one genuinely new filtering dimension this whole feature adds (design spec Decision 3) — this task is what actually uses it.

**Files:**
- Modify: `web/src/components/layout/CharactersPanel.vue`

- [ ] **Step 1: Replace the injection with a registration**

  Replace:
  ```ts
  import type { AggregateChange } from '@/composables/useChangeFeed'
  ```
  with:
  ```ts
  import { watchAggregate } from '@/composables/useAggregateWatch'
  import type { AggregateChange } from '@/composables/useChangeFeed'
  ```
  (both imports are needed: `AggregateChange` still types the `inject` call below.)

  Replace:
  ```ts
  const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
  if (lastAggregateChange) {
    watch(lastAggregateChange, (change) => {
      if (change?.aggregateType === 'character') refresh()
    })
  }
  ```
  with:
  ```ts
  watchAggregate({ type: 'character', campaignId: props.campaignId })
  const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
  if (lastAggregateChange) {
    watch(lastAggregateChange, (change) => {
      if (change?.aggregateType === 'character') refresh()
    })
  }
  ```

  The `watch(lastAggregateChange, ...)` block itself is unchanged — the backend now does the campaign-scoping (via the registered clause) before this component ever sees the change at all, so the existing "any character change → refresh" body is already correctly scoped by the time it runs.

- [ ] **Step 2: Typecheck**

  ```bash
  cd web && npm run typecheck
  ```

- [ ] **Step 3: Commit**

  ```bash
  git add web/src/components/layout/CharactersPanel.vue
  git commit -m "feat(web): CharactersPanel registers a campaign-scoped character watch"
  ```

---

### Task 7: `EntitiesPanel.vue` + `ObjectsPanel.vue` — universe-scoped `watchAggregate`

Both panels are near-identical universe-scoped search panels; the substitution is the same one-line pattern in each.

**Files:**
- Modify: `web/src/components/layout/EntitiesPanel.vue`
- Modify: `web/src/components/layout/ObjectsPanel.vue`

- [ ] **Step 1: `EntitiesPanel.vue`**

  Add the import:
  ```ts
  import { watchAggregate } from '@/composables/useAggregateWatch'
  ```

  Add the registration, right before the existing `lastAggregateChange` block:
  ```ts
  watchAggregate({ type: 'entity', universeId: props.universeId })
  const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
  ```

  Leave the rest of the `if (lastAggregateChange) { watch(...) }` block exactly as-is.

- [ ] **Step 2: `ObjectsPanel.vue`**

  Same pattern:
  ```ts
  import { watchAggregate } from '@/composables/useAggregateWatch'
  ```
  ```ts
  watchAggregate({ type: 'object', universeId: props.universeId })
  const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
  ```

- [ ] **Step 3: Typecheck**

  ```bash
  cd web && npm run typecheck
  ```

- [ ] **Step 4: Commit**

  ```bash
  git add web/src/components/layout/EntitiesPanel.vue web/src/components/layout/ObjectsPanel.vue
  git commit -m "feat(web): EntitiesPanel/ObjectsPanel register universe-scoped watches"
  ```

---

### Task 8: Detail views — `aggregateId`-scoped `watchAggregate`

`CharacterDetailView.vue`, `EntityDetailView.vue`, `ObjectDetailView.vue` each already filter on an exact `aggregateId` match today — the substitution is the same one-line pattern in all three, regardless of how different the surrounding component logic is.

**Files:**
- Modify: `web/src/views/CharacterDetailView.vue`
- Modify: `web/src/views/EntityDetailView.vue`
- Modify: `web/src/views/ObjectDetailView.vue`

- [ ] **Step 1: `CharacterDetailView.vue`**

  Add the import:
  ```ts
  import { watchAggregate } from '@/composables/useAggregateWatch'
  ```

  Add the registration right before the existing `lastAggregateChange` block. Note `characterId` is a `computed`, so the clause must be reactive too — wrap the registration in its own `watch`, re-registering (and unregistering the old one) whenever the route param changes, since `vue-router` reuses this component instance across sibling Character navigations (this file's own existing comment on `characterId` explains why):

  ```ts
  let unwatchCharacter: (() => void) | null = null
  watch(
    characterId,
    (id) => {
      unwatchCharacter?.()
      unwatchCharacter = watchAggregate({ type: 'character', aggregateId: id })
    },
    { immediate: true },
  )
  onUnmounted(() => unwatchCharacter?.())

  const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
  ```

  (`onUnmounted` is already imported in this file.)

- [ ] **Step 2: `EntityDetailView.vue`**

  Same reactive-id pattern as Step 1 (this file's `entityId` is also a `computed` for the same vue-router-reuse reason):
  ```ts
  import { watchAggregate } from '@/composables/useAggregateWatch'
  ```
  ```ts
  let unwatchEntity: (() => void) | null = null
  watch(
    entityId,
    (id) => {
      unwatchEntity?.()
      unwatchEntity = watchAggregate({ type: 'entity', aggregateId: id })
    },
    { immediate: true },
  )
  onUnmounted(() => unwatchEntity?.())

  const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
  ```

  This file does not currently import `onUnmounted` — replace its existing `import { computed, inject, onMounted, ref, watch, type Ref } from 'vue'` line with:
  ```ts
  import { computed, inject, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
  ```

- [ ] **Step 3: `ObjectDetailView.vue`**

  Same pattern, `objectId`:
  ```ts
  import { watchAggregate } from '@/composables/useAggregateWatch'
  ```
  ```ts
  let unwatchObject: (() => void) | null = null
  watch(
    objectId,
    (id) => {
      unwatchObject?.()
      unwatchObject = watchAggregate({ type: 'object', aggregateId: id })
    },
    { immediate: true },
  )
  onUnmounted(() => unwatchObject?.())

  const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
  ```

  This file also needs `onUnmounted` added — replace its existing `import { computed, inject, onMounted, ref, watch, type Ref } from 'vue'` line with:
  ```ts
  import { computed, inject, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
  ```

- [ ] **Step 4: Typecheck**

  ```bash
  cd web && npm run typecheck
  ```

- [ ] **Step 5: Commit**

  ```bash
  git add web/src/views/CharacterDetailView.vue web/src/views/EntityDetailView.vue web/src/views/ObjectDetailView.vue
  git commit -m "feat(web): detail views register aggregateId-scoped watches"
  ```

---

### Task 9: `CampaignOverviewPanel.vue` — `aggregateId`-scoped `watchAggregate`

Same reactive-id pattern as Task 8 (this file's `campaignId` is also a `computed` for the same vue-router-reuse reason, per its own existing comment).

**Files:**
- Modify: `web/src/views/CampaignOverviewPanel.vue`

- [ ] **Step 1: Add the import and reactive registration**

  ```ts
  import { watchAggregate } from '@/composables/useAggregateWatch'
  ```
  ```ts
  let unwatchCampaign: (() => void) | null = null
  watch(
    campaignId,
    (id) => {
      unwatchCampaign?.()
      unwatchCampaign = watchAggregate({ type: 'campaign', aggregateId: id })
    },
    { immediate: true },
  )
  onUnmounted(() => unwatchCampaign?.())

  const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
  ```

  (`onUnmounted` is already imported in this file; add this alongside its existing `onUnmounted(() => loadController?.abort())` rather than replacing it — two separate `onUnmounted` calls in the same `<script setup>` are fine, Vue runs every registered hook.)

- [ ] **Step 2: Typecheck**

  ```bash
  cd web && npm run typecheck
  ```

- [ ] **Step 3: Commit**

  ```bash
  git add web/src/views/CampaignOverviewPanel.vue
  git commit -m "feat(web): CampaignOverviewPanel registers an aggregateId-scoped watch"
  ```

---

### Task 10: `UniverseOverviewPanel.vue` — drop its redundant `useChangeFeed()` instance

This view currently runs a *second*, fully independent `useChangeFeed()` instance (its own polling loop, separate from `WorkspaceView.vue`'s) — the last remaining redundant instantiation this feature removes.

**Files:**
- Modify: `web/src/views/UniverseOverviewPanel.vue`

- [ ] **Step 1: Replace the change-feed block**

  Replace the existing `import { computed, onMounted, onUnmounted, ref, watch } from 'vue'` line with:
  ```ts
  import { computed, inject, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
  ```

  Replace:
  ```ts
  import { useChangeFeed } from '@/composables/useChangeFeed'
  ```
  with:
  ```ts
  import { watchAggregate } from '@/composables/useAggregateWatch'
  import type { AggregateChange } from '@/composables/useChangeFeed'
  ```

  Replace:
  ```ts
  const { lastChange: lastAggregateChange, start: startChangeFeed, stop: stopChangeFeed } = useChangeFeed()
  onMounted(() => startChangeFeed(universeId.value))
  watch(universeId, startChangeFeed)
  onUnmounted(stopChangeFeed)

  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'universe' && change.aggregateId.toLowerCase() === universeId.value.toLowerCase()) {
      load({ silent: true })
    }
  })
  ```
  with:
  ```ts
  let unwatchUniverse: (() => void) | null = null
  watch(
    universeId,
    (id) => {
      unwatchUniverse?.()
      unwatchUniverse = watchAggregate({ type: 'universe', aggregateId: id })
    },
    { immediate: true },
  )
  onUnmounted(() => unwatchUniverse?.())

  const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
  if (lastAggregateChange) {
    watch(lastAggregateChange, (change) => {
      if (change?.aggregateType === 'universe' && change.aggregateId.toLowerCase() === universeId.value.toLowerCase()) {
        load({ silent: true })
      }
    })
  }
  ```

- [ ] **Step 2: Typecheck**

  ```bash
  cd web && npm run typecheck
  ```

- [ ] **Step 3: Commit**

  ```bash
  git add web/src/views/UniverseOverviewPanel.vue
  git commit -m "refactor(web): UniverseOverviewPanel reads the shared feed instead of its own instance"
  ```

---

### Task 11: `UniversePickerView.vue` + `CampaignPickerView.vue` — close the two real gaps

These are the two views this whole feature exists to fix (design spec's Context section): today they load once, on mount, with no refresh mechanism at all.

**Files:**
- Modify: `web/src/views/UniversePickerView.vue`
- Modify: `web/src/views/CampaignPickerView.vue`

- [ ] **Step 1: `UniversePickerView.vue`**

  Replace the existing `import { onMounted, ref } from 'vue'` line with:
  ```ts
  import { inject, onMounted, ref, watch, type Ref } from 'vue'
  ```

  Add two new import lines:
  ```ts
  import { watchAggregate } from '@/composables/useAggregateWatch'
  import type { AggregateChange } from '@/composables/useChangeFeed'
  ```

  Add, after the existing `onMounted(...)` block (this is the bare "all universes" clause — Decision 3's `{ type: 'universe' }` with no scope at all):
  ```ts
  watchAggregate({ type: 'universe' })
  const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
  if (lastAggregateChange) {
    watch(lastAggregateChange, (change) => {
      if (change?.aggregateType === 'universe') list()
    })
  }
  ```

  No unregister/reactive-id handling is needed here — this clause has no scope to react to (it's the bare, universe-wide case), so a single `watchAggregate` call at setup time (auto-unregistered via `onScopeDispose` when this view unmounts) is sufficient, matching Task 2's own default behavior.

- [ ] **Step 2: `CampaignPickerView.vue`**

  Replace the existing `import { computed, onMounted, ref } from 'vue'` line with:
  ```ts
  import { computed, inject, onMounted, ref, watch, type Ref } from 'vue'
  ```

  Add two new import lines:
  ```ts
  import { watchAggregate } from '@/composables/useAggregateWatch'
  import type { AggregateChange } from '@/composables/useChangeFeed'
  ```

  Add, after the existing `onMounted(...)` block. `universeId` here is already a `computed` (see its own existing comment) — this clause is genuinely bound to that changing (a deep link from one Universe's campaign picker to another's), so it needs the same reactive re-registration pattern as Task 8/9:
  ```ts
  let unwatchCampaigns: (() => void) | null = null
  watch(
    universeId,
    (id) => {
      unwatchCampaigns?.()
      unwatchCampaigns = watchAggregate({ type: 'campaign', universeId: id })
    },
    { immediate: true },
  )

  const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
  if (lastAggregateChange) {
    watch(lastAggregateChange, (change) => {
      if (change?.aggregateType === 'campaign') listByUniverse(universeId.value)
    })
  }
  ```

  This file doesn't currently import `onUnmounted`, and doesn't need to here either — unlike Tasks 8/9/10, this view is never reused across a *different* Universe's picker via vue-router's param-only reuse in a way that would leak the old watch (a full remount happens on navigation between top-level picker instances in practice); if a reviewer determines vue-router does reuse this instance across a `universeId` change in some navigation path, add `onUnmounted(() => unwatchCampaigns?.())` the same way Task 8 does — treat this as a "verify against the router's actual behavior" checkpoint, not an assumption to leave unchecked.

- [ ] **Step 3: Typecheck**

  ```bash
  cd web && npm run typecheck
  ```

- [ ] **Step 4: Commit**

  ```bash
  git add web/src/views/UniversePickerView.vue web/src/views/CampaignPickerView.vue
  git commit -m "feat(web): UniversePickerView/CampaignPickerView refresh live, closing the two known gaps"
  ```

---

### Task 12: E2E test infrastructure — a real local SSE server for mocking `cmd/realtime`

**Spike finding (already validated, do not re-litigate):** Playwright's `page.route()`/`route.fulfill()` can only deliver a single, complete response body — it cannot push additional frames to an already-open connection, so it cannot simulate a live SSE push arriving after the page has already connected. A genuine local `http.Server` that keeps the response open and calls `res.write()` over time works perfectly against a real browser `EventSource` — confirmed directly in this sandbox: an `EventSource` connected to such a server received an initial frame, then received a second frame written by the test *after* the connection was already established, with no reconnect involved. This task builds that server as reusable e2e infrastructure. Separately confirmed: a `page.route()` callback that never calls `fulfill`/`continue`/`abort` leaves a real `EventSource` harmlessly `CONNECTING` forever (no `onerror`, no reconnect storm) — this is the safe default for the ~40 existing spec files that don't care about live push at all.

**Files:**
- Create: `web/e2e/support/mockRealtimeStream.ts`
- Modify: `web/e2e/support/mockBackend.ts`

**Interfaces:**
- Produces: `export interface MockRealtimeStream { origin: string; push(change: {...}): void; close(): Promise<void> }`
- Produces: `export async function startMockRealtimeStream(): Promise<MockRealtimeStream>`
- Modifies: `MockAuthConfig` gains an optional `realtimeOrigin?: string` field — **every one of the ~40 existing `installMockBackend(...)` call sites is unaffected** (an added optional field is backward compatible; none of them set it, so they all get the default "never resolve `/api/realtime/changes/stream`" behavior).

- [ ] **Step 1: Write the mock server**

  `web/e2e/support/mockRealtimeStream.ts`:

  ```ts
  import http from 'node:http'
  import type { AddressInfo } from 'node:net'

  export interface MockRealtimeChange {
    globalSeq: number
    aggregateType: string
    aggregateId: string
    eventType: string
    occurredAt: string
  }

  export interface MockRealtimeStream {
    // origin is the base URL (e.g. "http://127.0.0.1:54231") a test passes as
    // installMockBackend's realtimeOrigin — mockBackend.ts's route handler recognizes this
    // origin and calls route.continue() for it instead of intercepting, letting requests reach
    // this real server directly.
    origin: string
    // push fans one change out to every currently-connected client — there is normally exactly
    // one: the app's single EventSource (design spec Decision 2). This mock does not implement
    // cmd/realtime's own server-side WatchClause filtering (it ignores the `watch` query
    // parameter entirely) — give each test its own MockRealtimeStream instance and only push
    // events that test's own assertions actually care about, exactly like MockState.changes
    // already works for the catch-up-poll endpoints.
    push(change: MockRealtimeChange): void
    close(): Promise<void>
  }

  export async function startMockRealtimeStream(): Promise<MockRealtimeStream> {
    const clients: http.ServerResponse[] = []
    const server = http.createServer((req, res) => {
      if (req.url?.startsWith('/changes/stream')) {
        res.writeHead(200, {
          'Content-Type': 'text/event-stream',
          'Cache-Control': 'no-cache',
          Connection: 'keep-alive',
        })
        clients.push(res)
        req.on('close', () => {
          const i = clients.indexOf(res)
          if (i !== -1) clients.splice(i, 1)
        })
        return
      }
      res.writeHead(404)
      res.end()
    })
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
    const { port } = server.address() as AddressInfo
    return {
      origin: `http://127.0.0.1:${port}`,
      push(change) {
        const frame = `data: ${JSON.stringify(change)}\n\n`
        for (const res of clients) res.write(frame)
      },
      close: () =>
        new Promise<void>((resolve) => {
          for (const res of clients) res.end()
          server.close(() => resolve())
        }),
    }
  }
  ```

- [ ] **Step 2: Wire it into `mockBackend.ts`**

  In `web/e2e/support/mockBackend.ts`:

  Add `realtimeOrigin` to `MockAuthConfig`:
  ```ts
  export interface MockAuthConfig {
    baseURL: string
    authority: string
    clientId: string
    // realtimeOrigin, when set, points config.json's realtimeApiBaseUrl at a real
    // startMockRealtimeStream() server and makes the route handler below pass its requests
    // straight through to it (unintercepted). Unset (the default, every existing call site)
    // means requests to /api/realtime/changes/stream are left permanently pending — a real
    // EventSource against that is harmlessly stuck CONNECTING forever (verified: no onerror, no
    // reconnect storm), which is exactly correct for the many tests that don't exercise the
    // realtime feature at all and would otherwise see needless background reconnect noise.
    realtimeOrigin?: string
  }
  ```

  In `installMockBackend`, destructure the new field and use it in the `/config.json` response:
  ```ts
  export async function installMockBackend(page: Page, state: MockState, auth: MockAuthConfig): Promise<string[]> {
    const apiCalls: string[] = []
    const { baseURL, authority, clientId, realtimeOrigin } = auth
  ```
  ```ts
    if (p === '/config.json') {
      return json(route, {
        commandApiBaseUrl: `${baseURL}/api/command`,
        queryApiBaseUrl: `${baseURL}/api/query`,
        realtimeApiBaseUrl: realtimeOrigin ?? `${baseURL}/api/realtime`,
        oidc: {
          authority,
          clientId,
          redirectUri: `${baseURL}/login`,
          postLogoutRedirectUri: `${baseURL}/`,
        },
      })
    }
  ```

  Add the pass-through, before the existing `if (!p.startsWith('/api/')) return route.continue()` line (this must run for EVERY request, including ones that don't start with `/api/`, and must run before any other matching so a realtime request never falls through to the generic API-call bookkeeping below):
  ```ts
    if (realtimeOrigin && url.origin === realtimeOrigin) return route.continue()

    if (p === '/config.json') {
  ```

  Add the default (`realtimeOrigin` unset) fallback, as its own arm near the top of the function (immediately after the pass-through above and before the `/oidc/` handling is fine — order relative to `/oidc/`/`/config.json` doesn't matter since the path prefix is disjoint from both):
  ```ts
    if (p === '/api/realtime/changes/stream') {
      // Deliberately never call route.fulfill/continue/abort — see MockAuthConfig.realtimeOrigin's
      // doc comment above for why this is the correct, harmless default.
      return
    }
  ```

- [ ] **Step 3: Verify the ~40 existing call sites are genuinely unaffected**

  ```bash
  cd web && npm run typecheck
  npx playwright test --project=chromium --reporter=list
  ```

  Expect: full green, same pass count as before this task (confirm by running the suite once on the commit immediately before this task's change, if there's any doubt about the baseline count — this task must not change behavior for any existing spec).

- [ ] **Step 4: Commit**

  ```bash
  git add web/e2e/support/mockRealtimeStream.ts web/e2e/support/mockBackend.ts
  git commit -m "test(web-e2e): add a real local SSE server for mocking cmd/realtime"
  ```

---

### Task 13: Fix the 4 other e2e specs that depend on the removed polling interval

Task 3 deletes `useChangeFeed.ts`'s `setInterval` loop entirely. Four existing spec files (beyond `universe-change-feed.spec.ts`, handled in Task 14) simulate a background/hook-driven change by mutating `state.changes` directly and either waiting out the old 5-second poll interval or asserting the eventual pickup with a generous timeout — after Task 3, nothing ever delivers those mutations any more (the default mock realtime endpoint never resolves, per Task 12), so these tests would hang until their own assertion timeout and fail. This task converts each affected test (and only the affected ones — most tests in these files don't touch the change feed at all and need no changes) to push a live SSE frame via Task 12's `startMockRealtimeStream`.

**Files:**
- Modify: `web/e2e/campaign-configuration.spec.ts` (4 of its 6 tests)
- Modify: `web/e2e/campaign-creation-lag.spec.ts` (1 of its 3 tests)
- Modify: `web/e2e/character-stats-budget.spec.ts` (1 of its 10 tests)
- Modify: `web/e2e/character-traits.spec.ts` (2 of its 6 tests)

**Interfaces:**
- Consumes: `startMockRealtimeStream` from `web/e2e/support/mockRealtimeStream.ts` (Task 12).

- [ ] **Step 1: `campaign-configuration.spec.ts`**

  Add the import at the top of the file:
  ```ts
  import { startMockRealtimeStream } from './support/mockRealtimeStream'
  ```

  In **`'changing Max Stat Budget sends the setMaxStatBudget action and reflects the updated value once it lands'`**, add right after `const state = seedState()`:
  ```ts
  const realtime = await startMockRealtimeStream()
  ```
  add `realtimeOrigin: realtime.origin` to its `installMockBackend(...)` call, and replace:
  ```ts
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
  with:
  ```ts
    const campaign = state.campaigns.find((c) => c.id === 'c1')!
    campaign.configuration = JSON.stringify({ characterCreation: { maxStatBudget: 45 } })
    realtime.push({
      globalSeq: 1,
      aggregateType: 'campaign',
      aggregateId: 'c1',
      eventType: 'campaign.configuration_changed.v1',
      occurredAt: new Date().toISOString(),
    })

    // 4. the panel picks it up via the live push and the pending status clears
    await expect(page.getByText('Update requested')).not.toBeVisible()
    await expect(budgetInput).toHaveValue('45')
    await realtime.close()
  })
  ```

  In **`'an unrelated Campaign change while a save is pending does not revert the still-pending input or status'`**, add `const realtime = await startMockRealtimeStream()` and `realtimeOrigin: realtime.origin`, and replace:
  ```ts
    const campaign = state.campaigns.find((c) => c.id === 'c1')!
    campaign.name = 'Renamed Unrelated'
    state.changes.push({
      globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
      universeId: 'u1',
      aggregateType: 'campaign',
      aggregateId: 'c1',
      eventType: 'campaign.renamed.v1',
      occurredAt: new Date().toISOString(),
    })

    // Give the change-feed poll (5s interval) time to land and be (mis)handled.
    await page.waitForTimeout(6000)

    await expect(page.getByText('Update requested')).toBeVisible()
    await expect(budgetInput).toHaveValue('45')
  })
  ```
  with:
  ```ts
    const campaign = state.campaigns.find((c) => c.id === 'c1')!
    campaign.name = 'Renamed Unrelated'
    realtime.push({
      globalSeq: 1,
      aggregateType: 'campaign',
      aggregateId: 'c1',
      eventType: 'campaign.renamed.v1',
      occurredAt: new Date().toISOString(),
    })

    // Settle window: there is no confirming marker to assert on here (the point of this test is
    // that NOTHING changes), so a fixed wait is the correct tool — much shorter than the old
    // poll-interval wait now that delivery isn't gated by a 5s interval any more.
    await page.waitForTimeout(500)

    await expect(page.getByText('Update requested')).toBeVisible()
    await expect(budgetInput).toHaveValue('45')
    await realtime.close()
  })
  ```

  In **`'an unrelated Campaign change does not wipe an unsaved Max Stat Budget draft'`**, same setup addition, and replace:
  ```ts
    const campaign = state.campaigns.find((c) => c.id === 'c1')!
    campaign.name = 'Renamed Unrelated'
    state.changes.push({
      globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
      universeId: 'u1',
      aggregateType: 'campaign',
      aggregateId: 'c1',
      eventType: 'campaign.renamed.v1',
      occurredAt: new Date().toISOString(),
    })

    await page.waitForTimeout(6000)

    await expect(budgetInput).toHaveValue('99')
  })
  ```
  with:
  ```ts
    const campaign = state.campaigns.find((c) => c.id === 'c1')!
    campaign.name = 'Renamed Unrelated'
    realtime.push({
      globalSeq: 1,
      aggregateType: 'campaign',
      aggregateId: 'c1',
      eventType: 'campaign.renamed.v1',
      occurredAt: new Date().toISOString(),
    })

    await page.waitForTimeout(500)

    await expect(budgetInput).toHaveValue('99')
    await realtime.close()
  })
  ```

  In **`'Max Stat Budget updates when the configuration changes with no pending save in this session'`**, same setup addition, and replace:
  ```ts
    const campaign = state.campaigns.find((c) => c.id === 'c1')!
    campaign.configuration = JSON.stringify({ characterCreation: { maxStatBudget: 50 } })
    state.changes.push({
      globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
      universeId: 'u1',
      aggregateType: 'campaign',
      aggregateId: 'c1',
      eventType: 'campaign.configuration_changed.v1',
      occurredAt: new Date().toISOString(),
    })

    await expect(budgetInput).toHaveValue('50', { timeout: 10000 })
  })
  ```
  with:
  ```ts
    const campaign = state.campaigns.find((c) => c.id === 'c1')!
    campaign.configuration = JSON.stringify({ characterCreation: { maxStatBudget: 50 } })
    realtime.push({
      globalSeq: 1,
      aggregateType: 'campaign',
      aggregateId: 'c1',
      eventType: 'campaign.configuration_changed.v1',
      occurredAt: new Date().toISOString(),
    })

    await expect(budgetInput).toHaveValue('50')
    await realtime.close()
  })
  ```

  Leave **`'a save that never gets confirmed times out with an error and re-enables Save'`** and **`'a non-integer Max Stat Budget is rejected client-side with no request sent'`** completely untouched — neither pushes a change at all.

- [ ] **Step 2: `campaign-creation-lag.spec.ts`**

  Add the import at the top of the file:
  ```ts
  import { startMockRealtimeStream } from './support/mockRealtimeStream'
  ```

  Only **`'a change-feed reload during the initial lag neither strands the panel on "Loading…" nor blanks it once rendered'`** needs changes (the other two tests in this file rely purely on `waitForCampaign`'s own query-polling, unrelated to the change feed). Add `const realtime = await startMockRealtimeStream()` right after `const state = seedState(...)`, and `realtimeOrigin: realtime.origin` to its `installMockBackend(...)` call. Then replace:
  ```ts
    // This test's own state is fresh (nextId starts at 1) and this is the only create-Campaign
    // command it issues, so the new Campaign is deterministically 'campaign-1'. Push a matching
    // change directly onto state.changes, mirroring universe-change-feed.spec.ts's pattern of
    // simulating an externally-made change for the poller to pick up rather than going through a
    // command route.
    state.changes.push({
      globalSeq: 1,
      universeId: 'u1',
      aggregateType: 'campaign',
      aggregateId: 'campaign-1',
      eventType: 'campaign.created.v1',
      occurredAt: new Date().toISOString(),
    })
  ```
  with:
  ```ts
    // This test's own state is fresh (nextId starts at 1) and this is the only create-Campaign
    // command it issues, so the new Campaign is deterministically 'campaign-1'. Push a live SSE
    // frame directly — delivered near-instantly (well within the 8s lag window, same as the old
    // 5s-poll version's timing intent, just no longer gated by a poll interval).
    realtime.push({
      globalSeq: 1,
      aggregateType: 'campaign',
      aggregateId: 'campaign-1',
      eventType: 'campaign.created.v1',
      occurredAt: new Date().toISOString(),
    })
  ```

  And further down, replace:
  ```ts
    const created = state.campaigns.find((c) => c.id === 'campaign-1')!
    created.visibleAt = Date.now() + 999_999_999
    state.changes.push({
      globalSeq: 2,
      universeId: 'u1',
      aggregateType: 'campaign',
      aggregateId: 'campaign-1',
      eventType: 'campaign.renamed.v1',
      occurredAt: new Date().toISOString(),
    })
  ```
  with:
  ```ts
    const created = state.campaigns.find((c) => c.id === 'campaign-1')!
    created.visibleAt = Date.now() + 999_999_999
    realtime.push({
      globalSeq: 2,
      aggregateType: 'campaign',
      aggregateId: 'campaign-1',
      eventType: 'campaign.renamed.v1',
      occurredAt: new Date().toISOString(),
    })
  ```

  The trailing `await page.waitForTimeout(16_000)` and its two follow-up assertions are unaffected — that wait is timed against `load({silent:true})`'s own internal 15s `waitForCampaign` give-up, not against any change-feed delivery mechanism, and stays exactly as-is. Add `await realtime.close()` as the test's last line, after those two assertions.

- [ ] **Step 3: `character-stats-budget.spec.ts`**

  Add the import at the top of the file:
  ```ts
  import { startMockRealtimeStream } from './support/mockRealtimeStream'
  ```

  Only **`'Submit sends the submitPot payload with all 10 abbreviations, shows pending state, and closes once confirmed'`** needs changes. Add `const realtime = await startMockRealtimeStream()` right after `const state = seedState({ statBudget: 40 })`, and `realtimeOrigin: realtime.origin` to its `installMockBackend(...)` call. Replace:
  ```ts
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
  ```
  with:
  ```ts
    const character = state.characters.find((c) => c.id === 'ch1')!
    const info = JSON.parse(character.info)
    info.stats.attributes.ST.pot = 60
    info.stats.statBudget = 30
    character.info = JSON.stringify(info)
    realtime.push({
      globalSeq: 1,
      aggregateType: 'character',
      aggregateId: 'ch1',
      eventType: 'character.action_applied.v1',
      occurredAt: new Date().toISOString(),
    })

    await expect(dialog).not.toBeVisible()
    await expect(page.getByTestId('attribute-ST-pot')).toHaveText('60')
    await realtime.close()
  })
  ```

  Every other test in this file is untouched.

- [ ] **Step 4: `character-traits.spec.ts`**

  Add the import at the top of the file:
  ```ts
  import { startMockRealtimeStream } from './support/mockRealtimeStream'
  ```

  In **`'the traits row shows the held traits and an Add Trait control whose picker excludes them, and a full submit/pending/confirm round trip updates it'`**, add `const realtime = await startMockRealtimeStream()` right after `const state = seedState()`, and `realtimeOrigin: realtime.origin` to its `installMockBackend(...)` call. Replace:
  ```ts
    const character = state.characters.find((c) => c.id === 'ch1')!
    character.info = JSON.stringify({ stats: { traitPoints: 1, traits: ['agile', 'strong'] } })
    state.changes.push({
      globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
      universeId: 'u1',
      aggregateType: 'character',
      aggregateId: 'ch1',
      eventType: 'character.action_applied.v1',
      occurredAt: new Date().toISOString(),
    })

    // 4. the panel picks it up via the change-feed poll and the pending status clears
    await expect(page.getByText('Update requested — refreshing…')).not.toBeVisible({ timeout: 10000 })
    await expect(baseInfo.getByText('agile, strong', { exact: true })).toBeVisible()
  })
  ```
  with:
  ```ts
    const character = state.characters.find((c) => c.id === 'ch1')!
    character.info = JSON.stringify({ stats: { traitPoints: 1, traits: ['agile', 'strong'] } })
    realtime.push({
      globalSeq: 1,
      aggregateType: 'character',
      aggregateId: 'ch1',
      eventType: 'character.action_applied.v1',
      occurredAt: new Date().toISOString(),
    })

    // 4. the panel picks it up via the live push and the pending status clears
    await expect(page.getByText('Update requested — refreshing…')).not.toBeVisible()
    await expect(baseInfo.getByText('agile, strong', { exact: true })).toBeVisible()
    await realtime.close()
  })
  ```

  In **`'an unrelated Character change while an Add Trait request is pending does not revert the still-pending status'`**, add the same setup, and replace:
  ```ts
    const character = state.characters.find((c) => c.id === 'ch1')!
    character.name = 'Renamed Unrelated'
    state.changes.push({
      globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
      universeId: 'u1',
      aggregateType: 'character',
      aggregateId: 'ch1',
      eventType: 'character.renamed.v1',
      occurredAt: new Date().toISOString(),
    })

    // Give the change-feed poll (5s interval) time to land and be (mis)handled.
    await page.waitForTimeout(6000)

    await expect(page.getByText('Update requested — refreshing…')).toBeVisible()
  })
  ```
  with:
  ```ts
    const character = state.characters.find((c) => c.id === 'ch1')!
    character.name = 'Renamed Unrelated'
    realtime.push({
      globalSeq: 1,
      aggregateType: 'character',
      aggregateId: 'ch1',
      eventType: 'character.renamed.v1',
      occurredAt: new Date().toISOString(),
    })

    // Settle window — same reasoning as campaign-configuration.spec.ts's sibling test: there is no
    // confirming marker to assert on, since the point is that nothing changes.
    await page.waitForTimeout(500)

    await expect(page.getByText('Update requested — refreshing…')).toBeVisible()
    await realtime.close()
  })
  ```

  Every other test in this file (the no-traitPoints test, the timeout test, both table-layout tests, and the empty-traits-placeholder test) is untouched.

- [ ] **Step 5: Run all four files**

  ```bash
  cd web
  npx playwright test campaign-configuration.spec.ts campaign-creation-lag.spec.ts character-stats-budget.spec.ts character-traits.spec.ts --project=chromium --reporter=list
  ```

  Expect full green — same test count as before this task (only bodies changed, no tests added/removed).

- [ ] **Step 6: Commit**

  ```bash
  git add web/e2e/campaign-configuration.spec.ts web/e2e/campaign-creation-lag.spec.ts web/e2e/character-stats-budget.spec.ts web/e2e/character-traits.spec.ts
  git commit -m "test(web-e2e): convert the remaining poll-dependent specs to live SSE push"
  ```

---

### Task 14: Rewrite `universe-change-feed.spec.ts` for live push; add the two gap-closing tests

The existing spec's three tests simulate an externally-made change by mutating `state.changes`/`state.entities` directly and waiting up to 10s for the (now-removed) 5-second poll to notice — that mechanism no longer exists. This task converts them to push a live SSE frame via Task 12's mock server and assert near-instantly, and adds coverage for the two picker views this feature was built to fix, the Character-detail trait-hook scenario the original report named, a negative test proving the backend-side aggregateId filter genuinely discriminates, and a reconnect/catch-up-still-works case.

**Files:**
- Modify: `web/e2e/universe-change-feed.spec.ts`

**Interfaces:**
- Consumes: `startMockRealtimeStream` from `web/e2e/support/mockRealtimeStream.ts` (Task 12).

- [ ] **Step 1: Read the current file's three tests once more** (already shown above in this plan's research) before editing, so the seed data and assertions below map onto exactly the same fixtures.

- [ ] **Step 2: Replace the file**

  `web/e2e/universe-change-feed.spec.ts`:

  ```ts
  import { test, expect } from '@playwright/test'
  import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
  import { startMockRealtimeStream } from './support/mockRealtimeStream'
  import { seedAuth } from './support/auth'

  const CLIENT_ID = 'test-client'

  function seedState(overrides: Partial<MockState> = {}): MockState {
    return createMockState({
      universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
      campaigns: [
        { id: 'c1', universeId: 'u1', name: 'Test Campaign', rulesetId: 'r1', isArchived: false },
      ],
      rulesets: [{ id: 'r1', name: 'Test Ruleset' }],
      users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
      gamemasterIds: ['user-1'],
      characters: [
        { id: 'ch1', name: 'Aragorn', campaignId: 'c1', entityId: 'e1', playerUserId: 'user-1', isArchived: false },
      ],
      entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
      ...overrides,
    })
  }

  test('a live Entity change is picked up by the Entities sidebar without any local action or reload', async ({
    page,
    context,
    baseURL,
  }) => {
    const base = baseURL!
    const authority = `${base}/oidc`
    const state = seedState()
    const realtime = await startMockRealtimeStream()
    await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
    await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

    await page.goto('/universes/u1/campaigns/c1')
    const entitiesSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Entities' }) })
    await expect(entitiesSection.getByText('Aragorn')).toBeVisible()

    // Simulate another tab/user creating a new Entity in this Universe: add it to state (so the
    // panel's own re-search finds it) and push the live SSE frame directly — no poll, no wait.
    state.entities.push({ id: 'e2', name: 'Gandalf', universeId: 'u1', isArchived: false })
    realtime.push({
      globalSeq: 1,
      aggregateType: 'entity',
      aggregateId: 'e2',
      eventType: 'entity.created.v1',
      occurredAt: new Date().toISOString(),
    })

    await expect(entitiesSection.getByText('Gandalf')).toBeVisible()
    await realtime.close()
  })

  test('a batch of live changes updates every affected sidebar, not just the last one', async ({
    page,
    context,
    baseURL,
  }) => {
    const base = baseURL!
    const authority = `${base}/oidc`
    const state = seedState()
    const realtime = await startMockRealtimeStream()
    await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
    await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

    await page.goto('/universes/u1/campaigns/c1')
    const entitiesSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Entities' }) })
    const charactersSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Characters' }) })
    await expect(entitiesSection.getByText('Aragorn')).toBeVisible()

    // Regression for the original polling-era bug this file's history documents (Important #1):
    // creating a Character emits Entity+Character events together. Push both frames back to
    // back — Vue's flush:'pre' watch coalesces same-tick writes to a Ref, so an implementation
    // that only reacted to the LAST applied change (rather than each one via onmessage firing
    // per frame) would only refresh the Character sidebar, not the Entity one.
    state.entities.push({ id: 'e2', name: 'Gimli', universeId: 'u1', isArchived: false })
    state.characters.push({
      id: 'ch2',
      name: 'Legolas',
      campaignId: 'c1',
      entityId: 'e3',
      playerUserId: 'user-1',
      isArchived: false,
    })
    realtime.push({
      globalSeq: 1,
      aggregateType: 'entity',
      aggregateId: 'e2',
      eventType: 'entity.created.v1',
      occurredAt: new Date().toISOString(),
    })
    realtime.push({
      globalSeq: 2,
      aggregateType: 'character',
      aggregateId: 'ch2',
      eventType: 'character.created.v1',
      occurredAt: new Date().toISOString(),
    })

    await expect(entitiesSection.getByText('Gimli')).toBeVisible()
    await expect(charactersSection.getByText('Legolas')).toBeVisible()
    await realtime.close()
  })

  test('a live Universe rename is picked up by the Universe panel without any local action or reload', async ({
    page,
    context,
    baseURL,
  }) => {
    const base = baseURL!
    const authority = `${base}/oidc`
    const state = seedState()
    const realtime = await startMockRealtimeStream()
    await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
    await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

    await page.goto('/universes/u1/manage')
    await expect(page.getByRole('heading', { name: 'Test Universe' })).toBeVisible()

    // Open the Add Creator UserPicker before triggering the reload: this component lives inside
    // the template's `v-if="loading"` gate, so it would be unmounted (destroying its own
    // query-input state) if the change-driven reload below were not silent.
    await page.getByRole('button', { name: '+ Add' }).click()
    const userPicker = page.getByTestId('user-picker')
    await expect(userPicker).toBeVisible()

    state.universes[0].name = 'Renamed Elsewhere'
    realtime.push({
      globalSeq: 1,
      aggregateType: 'universe',
      aggregateId: 'u1',
      eventType: 'universe.renamed.v1',
      occurredAt: new Date().toISOString(),
    })

    await expect(page.getByRole('heading', { name: 'Renamed Elsewhere' })).toBeVisible()
    await expect(userPicker).toBeVisible()
    await realtime.close()
  })

  test('the Universe picker refreshes live when a Universe is created elsewhere', async ({ page, context, baseURL }) => {
    const base = baseURL!
    const authority = `${base}/oidc`
    const state = seedState({ universes: [] }) // empty: the real UniversePickerView renders its
    // grid immediately (no stored selection to redirect through) exactly when there's nothing to
    // redirect to — see UniversePickerView.vue's own onMounted logic.
    const realtime = await startMockRealtimeStream()
    await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
    await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Choose a Universe' })).toBeVisible()

    state.universes.push({ id: 'u9', name: 'New Universe', isArchived: false })
    realtime.push({
      globalSeq: 1,
      aggregateType: 'universe',
      aggregateId: 'u9',
      eventType: 'universe.created.v1',
      occurredAt: new Date().toISOString(),
    })

    await expect(page.getByRole('button', { name: 'New Universe' })).toBeVisible()
    await realtime.close()
  })

  test('the Campaign picker refreshes live when a Campaign is created elsewhere', async ({ page, context, baseURL }) => {
    const base = baseURL!
    const authority = `${base}/oidc`
    const state = seedState({ campaigns: [] })
    const realtime = await startMockRealtimeStream()
    await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
    await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

    await page.goto('/universes/u1')
    await expect(page.getByRole('heading', { name: 'Choose a Campaign' })).toBeVisible()

    state.campaigns.push({ id: 'c9', universeId: 'u1', name: 'New Campaign', rulesetId: 'r1', isArchived: false })
    realtime.push({
      globalSeq: 1,
      aggregateType: 'campaign',
      aggregateId: 'c9',
      eventType: 'campaign.created.v1',
      occurredAt: new Date().toISOString(),
    })

    await expect(page.getByRole('button', { name: 'New Campaign' })).toBeVisible()
    await realtime.close()
  })

  test('a live character.info_changed.v1 update refreshes an open Character detail page without a reload', async ({
    page,
    context,
    baseURL,
  }) => {
    // This is the exact scenario named in the original report this whole feature exists to fix:
    // "incremental changes like the results of the hooks from adding traits are not picked up."
    const base = baseURL!
    const authority = `${base}/oidc`
    const state = seedState({
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
      characters: [
        {
          id: 'ch1',
          name: 'Aragorn',
          campaignId: 'c1',
          entityId: 'e1',
          playerUserId: 'user-1',
          isArchived: false,
          info: JSON.stringify({ stats: { traitPoints: 2, traits: ['agile'] } }),
        },
      ],
    })
    const realtime = await startMockRealtimeStream()
    await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
    await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

    await page.goto('/universes/u1/campaigns/c1/characters/ch1')
    const baseInfo = page.getByTestId('base-info-card')
    await expect(baseInfo.getByText('agile', { exact: true })).toBeVisible()

    // Simulate a trait-hook side effect landing with no local action taken on this page at all —
    // a different tab/session triggered it entirely.
    const character = state.characters.find((c) => c.id === 'ch1')!
    character.info = JSON.stringify({ stats: { traitPoints: 1, traits: ['agile', 'strong'] } })
    realtime.push({
      globalSeq: 1,
      aggregateType: 'character',
      aggregateId: 'ch1',
      eventType: 'character.info_changed.v1',
      occurredAt: new Date().toISOString(),
    })

    await expect(baseInfo.getByText('agile, strong', { exact: true })).toBeVisible()
    await realtime.close()
  })

  test('a live change to a DIFFERENT Character does not reload an open, unrelated Character detail page', async ({
    page,
    context,
    baseURL,
  }) => {
    // Proves the backend-side aggregateId filter (Task 8's watchAggregate({type:'character',
    // aggregateId})) genuinely discriminates — not just "any character event reaches this page
    // and it happens to render the same thing anyway". Success is IN-visibility of a marker this
    // page would show if (incorrectly) reloaded.
    const base = baseURL!
    const authority = `${base}/oidc`
    const state = seedState({
      characters: [
        { id: 'ch1', name: 'Aragorn', campaignId: 'c1', entityId: 'e1', playerUserId: 'user-1', isArchived: false },
        { id: 'ch2', name: 'Legolas', campaignId: 'c1', entityId: 'e2', playerUserId: 'user-1', isArchived: false },
      ],
      entities: [
        { id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false },
        { id: 'e2', name: 'Legolas', universeId: 'u1', isArchived: false },
      ],
    })
    const realtime = await startMockRealtimeStream()
    await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
    await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

    await page.goto('/universes/u1/campaigns/c1/characters/ch1')
    const baseInfo = page.getByTestId('base-info-card')
    await expect(baseInfo.getByText('Aragorn', { exact: true })).toBeVisible()

    // Rename the OTHER Character and push its event — if this page's own watch clause were not
    // actually scoped to ch1's aggregateId (e.g. a regression back to "any character in this
    // campaign"), CharacterDetailView would silently reload and start showing Legolas's data.
    const other = state.characters.find((c) => c.id === 'ch2')!
    other.name = 'Renamed Legolas'
    realtime.push({
      globalSeq: 1,
      aggregateType: 'character',
      aggregateId: 'ch2',
      eventType: 'character.renamed.v1',
      occurredAt: new Date().toISOString(),
    })

    // Generous settle window with nothing to wait ON (no marker of the bad outcome exists to poll
    // for) — this is an absence assertion, so a fixed wait is the correct tool here even though
    // it's slower than the rest of this file's push-and-immediately-assert tests.
    await page.waitForTimeout(1000)
    await expect(page.getByText('Renamed Legolas')).not.toBeVisible()
    await expect(baseInfo.getByText('Aragorn', { exact: true })).toBeVisible()
    await realtime.close()
  })

  test('a dropped SSE connection still catches up via the polling fallback on reconnect', async ({
    page,
    context,
    baseURL,
  }) => {
    const base = baseURL!
    const authority = `${base}/oidc`
    const state = seedState()
    const realtime = await startMockRealtimeStream()
    await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
    await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

    await page.goto('/universes/u1/campaigns/c1')
    const entitiesSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Entities' }) })
    await expect(entitiesSection.getByText('Aragorn')).toBeVisible()

    // Simulate a change happening WHILE the connection is down: close the mock server's
    // connection (the page's EventSource will see this as a drop and auto-reconnect per spec,
    // default ~3s retry) without ever pushing a live frame for it — the only way this change can
    // reach the page is via the catch-up poll that fires on the reconnect's onopen.
    state.entities.push({ id: 'e2', name: 'Bilbo', universeId: 'u1', isArchived: false })
    state.changes.push({
      globalSeq: 1,
      universeId: 'u1',
      aggregateType: 'entity',
      aggregateId: 'e2',
      eventType: 'entity.created.v1',
      occurredAt: new Date().toISOString(),
    })
    await realtime.close()

    // No realtime.push() call for this one — proves the catch-up poll (not a live frame) is what
    // delivers it. Generous timeout: covers the browser's own auto-reconnect delay.
    await expect(entitiesSection.getByText('Bilbo')).toBeVisible({ timeout: 10000 })
  })
  ```

- [ ] **Step 3: Run the suite**

  ```bash
  cd web && npx playwright test universe-change-feed.spec.ts --project=chromium --reporter=list
  ```

  Expect all 8 tests green. If the last test (dropped-connection reconnect) is flaky on the default ~3s browser retry timing, that is useful signal, not a test bug to paper over — investigate via `systematic-debugging` before adjusting the timeout, since a consistently-failing reconnect would mean Task 3's `onopen`-driven catch-up isn't actually firing on the browser's own silent reconnect, a real regression against this plan's Global Constraints.

- [ ] **Step 4: Run the full e2e suite once more**

  ```bash
  npx playwright test --project=chromium --reporter=list
  ```

  Expect full green, matching (or improving on) the pre-existing pass count.

- [ ] **Step 5: Commit**

  ```bash
  git add web/e2e/universe-change-feed.spec.ts
  git commit -m "test(web-e2e): rewrite change-feed spec for live SSE push; close the two picker gaps"
  ```

---

## Final Verification (after all tasks)

```bash
cd web
npm run typecheck
npm run build
npx playwright test --project=chromium --reporter=list
cd ../test/e2e && go build ./... && go vet ./...
```

All four must be clean. `make dev-up`/`make test-e2e` (real cluster) are deliberately **not** part of this plan's own task loop: a `kind` cluster with a `timadorus-dev` namespace already exists in this environment, but it is a machine-wide resource potentially shared with other concurrent worktree sessions, not something scoped to this worktree — redeploying over it could disrupt another session's in-progress work. Task 1's `helm template` check (its own Step 6) is sufficient to prove the new Gateway routes render correctly; do **not** run `make dev-up`/`helm upgrade` against the shared cluster as part of automated task execution. If a live end-to-end smoke test against a real deployed backend is wanted, surface that as an explicit question for the human partner rather than doing it unprompted.
