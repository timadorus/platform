# Universe Panel (replacing the "Manage Universe" modal) — Design

## Context

Today, clicking the 🌍 universe badge in `AppHeader` (rendered whenever `universeName` is set,
which is both inside `WorkspaceView` and on `CampaignPickerView`) opens `ManageUniverseModal.vue`
— a modal with Rename, a Creators list (add/remove via `UserPicker`), and an Archive button.
Only `WorkspaceView` currently wires the badge's click handler to open it; `CampaignPickerView`'s
badge renders identically but is inert.

This mirrors the `campaign-tabbed-panel` branch's precedent almost exactly (a modal replaced by a
routed panel), but with one deliberate difference confirmed during brainstorming: the Campaign
panel stayed nested inside `WorkspaceView` (sidebar visible, since you're still "in" that
Campaign). The Universe panel instead **navigates away entirely** — no Characters/Entities/Objects
sidebar makes sense at the Universe level, since a Universe spans multiple Campaigns.

## Decisions

- **New standalone route**, a sibling of `campaign-picker`/`universe-picker`, not nested in
  `WorkspaceView`:
  ```
  path: '/universes/:universeId/manage'
  name: 'universe-overview'
  component: UniverseOverviewPanel.vue
  ```
  Own `AppHeader` (universe badge only — no `campaign-name` prop, matching
  `CampaignPickerView`'s header shape), no sidebar.

- **The universe badge is wired to navigate here from both places it currently renders**:
  `WorkspaceView.vue` (replacing today's `showManageUniverse = true` with a `router.push`, exactly
  mirroring `goToCampaignOverview`'s existing shape) and `CampaignPickerView.vue` (currently
  inert — this closes that latent inconsistency, a button that already looks clickable but does
  nothing).

- **Single flat panel, no tabs.** Unlike the Campaign panel (Manage/Configuration), there's no
  second content area to tab into here, so `BaseTabs` is not used.

- **Click-to-reveal Rename**, matching the established convention from `ManageCampaignPanel.vue`/
  `BaseInfoTable.vue` (plain text + "Rename" button → input + Save/Cancel) rather than the old
  modal's always-editable input. This is the same modal→panel migration trade-off already made and
  documented (`docs/BACKLOG.md`) for the Campaign case; not a new decision, just continuing the
  established pattern.

- **Creators section is unchanged in behavior** from the modal — list + "+ Add" (`UserPicker`) +
  per-row Remove, same `useUniverses()` calls (`listCreators`/`addCreator`/`removeCreator`).

- **New: a "Campaigns" list group** — every non-archived Campaign in this Universe
  (`useCampaigns().listByUniverse`, which already excludes archived by default at the query-API
  level), rendered as a simple list (styled like `ManageCampaignPanel.vue`'s Gamemasters list, not
  the `AggregatePickerGrid` picker widget, since there's no "+ Create Campaign" affordance in
  scope here). Each row is clickable and navigates straight into that Campaign's workspace
  (`router.push({ name: 'workspace', params: { universeId, campaignId } })`).

- **Archive button + confirm dialog unchanged** from the modal (`ConfirmDialog`, same copy,
  navigates to `universe-picker` on success — mirroring `WorkspaceView`'s existing
  `onUniverseArchived` behavior).

- **No "back" link added.** Consistent with `CampaignPickerView`'s own precedent (no link back to
  `universe-picker` either) — browser back, or clicking a Campaign in the new list, are the ways
  out. Not adding one is a deliberate scope match, not an oversight.

## Changes

### `web/src/router/index.ts`

Add the new route (sibling of `campaign-picker`/`universe-picker`, not nested):
```ts
{
  path: '/universes/:universeId/manage',
  name: 'universe-overview',
  component: () => import('@/views/UniverseOverviewPanel.vue'),
  props: true,
},
```

### `web/src/views/UniverseOverviewPanel.vue` (NEW)

Self-contained routed view (mirroring `CampaignPickerView.vue`'s structure — its own `AppHeader`,
own load(), own API calls via `useUniverses()`/`useUsers()`/`useCampaigns()`), combining
`ManageUniverseModal.vue`'s current logic (rename/creators/archive) with a new Campaigns list.

### `web/src/components/modals/ManageUniverseModal.vue` — DELETED

Repo-wide grep confirms exactly two references before this change (`WorkspaceView.vue`'s import
and usage) — both removed in the same change.

### `web/src/views/WorkspaceView.vue`

Remove `ManageUniverseModal` import, `showManageUniverse` ref, `onUniverseRenamed`/
`onUniverseArchived` functions, and the modal's template block. Add:
```ts
function goToUniverseOverview() {
  router.push({ name: 'universe-overview', params: { universeId: universeId.value } })
}
```
bound as `@click-universe-badge="goToUniverseOverview"` (replacing
`@click-universe-badge="showManageUniverse = true"`).

### `web/src/views/CampaignPickerView.vue`

Add the same wiring: a `goToUniverseOverview` function and
`@click-universe-badge="goToUniverseOverview"` on its `AppHeader` (currently unbound).

## Explicitly Out of Scope

- Any backend/API change — this is entirely a frontend routing/component change, reusing existing
  `useUniverses()`/`useCampaigns()` composable functions as-is.
- Creating a Campaign from this panel (the new Campaigns list has no "+ Create" affordance).
- A "back to Universe picker" link.
- Any retry/timeout polling for eventual consistency on this view (matching the Campaign panel's
  own explicit scope decision from the prior branch — this view wasn't reported as having that
  problem).
