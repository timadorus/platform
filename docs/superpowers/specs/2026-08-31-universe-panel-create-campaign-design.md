# Universe Panel: Create Campaign Button — Design

## Context

`UniverseOverviewPanel.vue`'s "Campaigns" section lists the active Universe's Campaigns
(clickable, navigating into each one) but has no way to create a new one — creation is currently
only reachable via `CampaignPickerView.vue`'s "+ Create Campaign" tile.

## Decision

Add a `BaseButton` below the Campaigns list (inside the same section, below the `<ul>`/"No
Campaigns yet." block, visible regardless of whether the list is empty), reusing the existing
`CreateCampaignModal.vue` as-is — no new component, no composable changes. On the modal's
`created` event, close it and call the panel's existing `goToCampaign(id)` (already
selection-aware), navigating straight into the new Campaign's workspace — matching exactly what
`CampaignPickerView.vue`'s own create flow already does, not staying on this panel to refresh the
list.

## Changes

### `web/src/views/UniverseOverviewPanel.vue`

- Import `CreateCampaignModal.vue`.
- Add `const showCreateCampaign = ref(false)`.
- Add `function onCampaignCreated(id: string) { showCreateCampaign.value = false; goToCampaign(id) }`.
- Template: a `BaseButton` ("+ Create Campaign") below the Campaigns `<ul>`/empty-state paragraph,
  setting `showCreateCampaign.value = true`; the modal itself, `v-if="showCreateCampaign"`, with
  `:universe-id="universeId"`, `@close="showCreateCampaign = false"`,
  `@created="onCampaignCreated"`.

## Explicitly Out of Scope

- Any change to `CreateCampaignModal.vue` itself, `useCampaigns.ts`, or the backend.
- Refreshing the Campaigns list in place instead of navigating away (rejected design alternative —
  confirmed with the user).
