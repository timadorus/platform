# Universe Overview Panel — Live Change-Feed Consumer

## Context

`docs/BACKLOG.md`'s Web SPA section (and a comment already left in
`UniverseOverviewPanel.vue:1-6`) documents that this panel receives no live change-feed updates:
it's a top-level route (`/universes/:universeId/manage`), not a child route of `WorkspaceView`, so
`WorkspaceView`'s `provide('lastAggregateChange', ...)` never reaches it via `inject`. Renaming or
archiving a Universe (or changing its Creators) from another tab/session never reflects here without
a manual reload.

Per the user's explicit direction, this uses the panel's-own-`useChangeFeed()`-instance approach
(the BACKLOG entry's first alternative), not the BACKLOG's other suggestion of hoisting the
`provide` to a shared ancestor — `useChangeFeed()` is a self-contained factory (own cursor/timer/
epoch state per call), so a second independent instance here needs no changes to `WorkspaceView`'s
route structure or provide scope at all.

## Fix

In `UniverseOverviewPanel.vue`:
1. Import and call `useChangeFeed()` directly (not via `inject`) — same composable, a fresh
   independent instance.
2. `onMounted(() => startChangeFeed(universeId.value))`, `watch(universeId, startChangeFeed)`,
   `onUnmounted(stopChangeFeed)` — mirroring `WorkspaceView.vue`'s exact lifecycle wiring.
3. Give `load()` the same `opts: { silent?: boolean } = {}` shape `CampaignOverviewPanel.vue`'s
   `load` already has (`if (!opts.silent) loading.value = true` / `... = false` around the existing
   body) — needed so a background refresh doesn't flash the whole panel's `v-if="loading"` gate.
4. Add a `watch(lastAggregateChange, ...)` filtering strictly on
   `change?.aggregateType === 'universe' && change.aggregateId.toLowerCase() === universeId.value.toLowerCase()`,
   calling `load({ silent: true })` — the exact same single-aggregate-self filter shape
   `CampaignOverviewPanel.vue`'s own `lastAggregateChange` watch already uses for `'campaign'`.

Deliberately scoped to `'universe'`-type changes only (renaming, archiving, Creator add/remove) —
not `'campaign'`-type changes affecting this panel's Campaigns list. That would be a second,
separate feature (refreshing a *different* list on a *different* aggregate type's changes) and no
existing consumer in this codebase does that composite pattern; out of scope for this fix, matching
YAGNI.

## Testing

New Playwright test in `web/e2e/universe-change-feed.spec.ts` (already the home for this pattern —
extend it rather than creating a new file), mirroring its existing two tests' shape:
1. Navigate to `/universes/u1/manage`, confirm the current Universe name renders.
2. Simulate an external rename: mutate `state.universes[0].name` directly and push a matching
   `state.changes` row (`aggregateType: 'universe'`, `aggregateId: 'u1'`).
3. Wait past the 5s poll interval and assert the panel's heading now shows the new name — proving
   the panel picked up the change with no local action, exactly like the file's existing Entity/
   Character tests.

## Out of Scope

Reacting to `'campaign'`-type changes to refresh this panel's Campaigns list (a separate, unrequested
feature); any change to `WorkspaceView.vue`'s provide scope or route structure.
