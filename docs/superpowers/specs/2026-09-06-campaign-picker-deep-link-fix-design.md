# CampaignPickerView Deep-Link Selection Fix

## Context

`docs/BACKLOG.md`'s Web SPA section documents a real, reproducible bug: `CampaignPickerView.vue`'s
`goTo()` calls only `selection.setCampaign(campaignId)`, unlike its sibling
`UniverseOverviewPanel.vue`'s `goToCampaign()`, which correctly calls
`selection.setUniverse(universeId)` first (`selection.ts`'s `setUniverse` nulls the stored
`selectedCampaignId`, so campaign-after-universe is the only correct call order). On a deep link
straight to a different Universe's campaign picker (bypassing the Universe picker, which is the only
normal entry point that already calls `setUniverse`), `CampaignPickerView` persists a Campaign
selection paired with the stale, previously-stored Universe id — corrupting the user's restored
selection on the next cold boot.

## Fix

Add `selection.setUniverse(universeId.value)` as the first line of `CampaignPickerView.vue`'s
`goTo(campaignId: string)`, before the existing `selection.setCampaign(campaignId)` call — matching
`goToCampaign`'s exact shape. This is safe for all three existing call sites of `goTo`:
- The stored-selection restore path (`onMounted`, when `selection.selectedUniverseId` already equals
  `universeId.value`): `setUniverse` with the same value is a no-op for that field, then
  `setCampaign` sets the correct campaign back — unchanged behavior.
- The picker grid's `@select="goTo"`.
- `onCreated`, after creating a new Campaign.

## Testing

New Playwright spec (`web/e2e/campaign-picker-deep-link.spec.ts`) reproducing the exact sequence from
the BACKLOG entry:
1. Seed mock backend state with two Universes (`u1`, `u2`), each with one Campaign.
2. Seed auth (`seedAuth`, subject is always `test-sub` per `support/auth.ts`).
3. Pre-seed `localStorage['timadorus:selection:test-sub']` (via `context.addInitScript`, mirroring
   `seedAuth`'s own pattern) to `{selectedUniverseId: 'u1', selectedCampaignId: null}` — simulating a
   prior session that had `u1` selected.
4. Navigate directly to `/universes/u2/campaigns` (the deep link — skips `u1` and the Universe picker
   entirely).
5. Select `u2`'s existing Campaign from the picker grid.
6. Assert `localStorage['timadorus:selection:test-sub']` now holds
   `{selectedUniverseId: 'u2', selectedCampaignId: '<u2's campaign id>'}` — before the fix, this
   would incorrectly still read `selectedUniverseId: 'u1'`.
7. Reload from `/` and confirm the app restores straight into `u2`'s campaign workspace (proving the
   stored pair is no longer mismatched and doesn't get cleared on the next boot).

No new mock-backend route arms are needed — `GET /universes/:universeId`,
`GET /universes/:universeId/campaigns`, and `GET /campaigns/:campaignId` are already mocked.

## Out of Scope

Any other selection-store call site; `UniverseOverviewPanel.goToCampaign` itself (already correct,
unchanged).
