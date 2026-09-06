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
4. Navigate directly to `/universes/u2` (`CampaignPickerView`'s actual route — confirmed via
   `web/src/router/index.ts`: the `campaign-picker` route's path is `/universes/:universeId`, with
   no `/campaigns` suffix) — the deep link, skipping `u1` and the Universe picker
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

## Addendum: a second, adjacent bug found during implementation

Writing the regression test's final step (reload from `/`, confirm the restored pair is no longer
mismatched) exposed a second, real bug in the same family, in a sibling file:
`UniversePickerView.vue`'s own stored-selection restore path (`onMounted`) called
`goTo(existing.id)` even when restoring an *already-correct* existing Universe id — and `goTo` calls
`selection.setUniverse(id)`, which unconditionally nulls `selectedCampaignId`. So reloading from `/`
with a valid, matching stored Campaign selection would silently wipe it before `CampaignPickerView`
ever got a chance to restore it — the exact same bug shape this branch exists to fix, on the
"restore an existing selection" path rather than the "select from the grid" path.

Fixed by replacing that call with a plain `router.push({ name: 'campaign-picker', params:
{ universeId: existing.id } })` — no `selection.setUniverse` call at all, since the Universe id
being restored is by definition already the one already stored; `CampaignPickerView`'s own
`onMounted` then correctly restores the paired Campaign selection.

This was independently verified (not just trusted from the implementer's report): no other e2e spec
exercises `UniversePickerView`'s restore path or asserts on `selectedCampaignId`, so nothing relied
on the old, buggy clearing behavior; the full 26-test e2e suite passes.

A related test-infrastructure fix was also needed: Playwright's `context.addInitScript` re-runs on
*every* navigation in a context, not just the first — so this test's second `page.goto('/')` call
re-seeded `localStorage` back to the stale `{selectedUniverseId: 'u1', ...}` value, clobbering
whatever the app had legitimately persisted after the first navigation. Fixed by guarding the seed
script to only write if the key doesn't already exist.

