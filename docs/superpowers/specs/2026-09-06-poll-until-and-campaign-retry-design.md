# Shared `pollUntil` Helper + Campaign Creation Retry/Timeout Parity

## Context

`docs/BACKLOG.md`'s Web SPA section flags two related items, deliberately paired here per the
BACKLOG's own note that Campaign creation's retry gap would be the fifth copy of the poll-with-
timeout skeleton — the trigger the BACKLOG itself names for extracting a shared helper:

1. **Duplicated poll skeleton** — `useUsers.ts`'s `waitForUser`, `useCharacters.ts`'s
   `waitForCharacter`/`waitForCharacterInList`, and `useEntities.ts`'s `waitForEntityInList` all
   share an identical `for (;;) { check abort; do fetch; check abort; check error/found; check
   deadline; sleep }` skeleton, varying only in what they fetch and what counts as success.
2. **No retry/timeout handling for Campaign creation** — `CampaignOverviewPanel.vue`'s `load()`
   calls `getCampaign()` once; a lagging read model right after Campaign creation shows a dead-end
   "Campaign not found." with no Retry, unlike Character creation's
   `waitForCharacter`/`loadTimedOut`/Retry pattern. `WorkspaceView.vue`'s own `getCampaign` call has
   the same exposure for the header badge.

## Design

### `usePolling.ts` — the shared helper

Confirmed by reading all four existing implementations: they fall into exactly two shapes.

- **Get-by-id shape** (`waitForCharacter`): the fetch function itself already returns `T | null`
  (`null` on any failure, 404 included — no separate reactive error to check).
- **List-membership shape** (`waitForUser`, `waitForCharacterInList`, `waitForEntityInList`): the
  fetch function is `void`-returning and sets a reactive `error` ref on a real failure, distinct
  from "not yet in the list" — a real error must stop polling immediately, not retry to the
  deadline.

`pollUntil<T>` covers both via an optional `shouldStop` callback, checked at the exact point the
original code checked `error.value` (immediately after the fetch, before checking whether the item
was found — preserving the original ordering, since an error taking priority over a stale prior
"found" state was the original's implicit behavior):

```ts
export interface PollOptions {
  intervalMs?: number
  timeoutMs?: number
  signal?: AbortSignal
  shouldStop?: () => boolean
}

export async function pollUntil<T>(
  attempt: () => Promise<T | null | undefined>,
  opts: PollOptions = {},
): Promise<T | null> {
  const intervalMs = opts.intervalMs ?? 750
  const timeoutMs = opts.timeoutMs ?? 15000
  const deadline = Date.now() + timeoutMs
  for (;;) {
    if (opts.signal?.aborted) return null
    const result = await attempt()
    if (opts.signal?.aborted) return null
    if (opts.shouldStop?.()) return null
    if (result !== null && result !== undefined) return result
    if (Date.now() >= deadline) return null
    await new Promise((resolve) => setTimeout(resolve, intervalMs))
  }
}
```

Each existing `waitForX` becomes a thin wrapper:

- `waitForCharacter(id, opts)` → `return pollUntil(() => get(id), opts)` (get-by-id shape, no
  `shouldStop` needed — `get()` already collapses every failure to `null`).
- `waitForUser(id, opts)` → wraps the list-membership shape:
  ```ts
  const found = await pollUntil(async () => {
    await list()
    return users.value.some((u) => u.id === id) ? true : null
  }, { ...opts, shouldStop: () => error.value !== null })
  return found ?? false
  ```
- `waitForCharacterInList` and `waitForEntityInList` follow the identical list-membership shape,
  substituting their own `list`/`search` call and membership check.

This is a pure refactor — every existing caller's signature and behavior stays identical; only the
internal implementation changes.

### `waitForCampaign` — the fifth copy, extracted from day one

Added to `useCampaigns.ts`, following the get-by-id shape exactly like `waitForCharacter` (confirmed
`useCampaigns.ts`'s existing `get()` already collapses every failure to `null`, same as
`useCharacters.ts`'s `get()`):

```ts
async function waitForCampaign(
  id: string,
  opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
): Promise<CampaignSummary | null> {
  return pollUntil(() => get(id), opts)
}
```

### `CampaignOverviewPanel.vue` — Retry/timeout parity with `CharacterDetailView.vue`

- `load()` switches from a single `getCampaign(campaignId.value)` call to
  `waitForCampaign(campaignId.value, { signal })`, gaining the same `AbortController`-based
  cancellation `CharacterDetailView.vue` already uses (needed now that `load()` can genuinely take
  up to 15s — a stale in-flight load for a previous `campaignId` must not land after navigating to
  a new one and overwrite the page).
- New `loadTimedOut` ref, reset at the top of every `load()` call, set `true` only on a non-silent
  timeout (mirroring `CharacterDetailView.vue`'s exact silent/non-silent distinction — a background
  change-feed-triggered reload that happens to time out must not blow away an already-rendered page).
- New template branch between the existing `v-else-if="campaign"` and the final `v-else` "Campaign
  not found.": a `v-else-if="loadTimedOut"` branch with a Retry button and a "Back to Universe" link
  (`{ name: 'universe-overview', params: { universeId } }` — confirmed this route name maps to
  `UniverseOverviewPanel.vue`), matching `CharacterDetailView.vue`'s Retry/Back-to-X shape.
- The existing `getRuleset(campaign.value.rulesetId)` ternary (`campaign.value ? ... :
  Promise.resolve(null)`) simplifies to an unconditional call, since it now only runs inside the
  "found" branch where the type is already narrowed non-null.

### `WorkspaceView.vue` — header badge parity

`load()`'s `campaign.value = await getCampaign(campaignId.value)` becomes
`campaign.value = await waitForCampaign(campaignId.value)` — no UI change needed here (there's no
"not found" state to render in the workspace shell itself; the badge just stays blank a bit longer
while polling instead of blanking permanently on a lag).

## Accepted trade-off

This adds a second and third independent 750ms poller (`CampaignOverviewPanel` and `WorkspaceView`)
alongside `CampaignOverviewPanel`'s other data fetches during the post-creation lag window — the
same accepted-by-design multiplication already documented for Character creation (BACKLOG's "Three
independent 750ms polls" entry, explicitly not being revisited here).

## Testing

**Mock backend extension needed first** (confirmed by reading `web/e2e/support/mockBackend.ts`):
`createVisibilityDelayMs` currently only gates Character/Entity visibility — `POST
/universes/:universeId/campaigns` pushes the new Campaign with no `visibleAt`, and `GET
/campaigns/:campaignId` has no visibility check at all. Extend the same mechanism to Campaigns
(reuse `createVisibilityDelayMs`, do not add a second config knob):
- Add `visibleAt?: number` to `MockCampaign` (same optional-for-backward-compatibility shape as
  `MockCharacter`'s).
- `POST /universes/:universeId/campaigns`: compute `visibleAt` the same way the Character-creation
  handler already does (`state.createVisibilityDelayMs !== undefined ? Date.now() +
  state.createVisibilityDelayMs : undefined`) and set it on the pushed Campaign.
- `GET /campaigns/:campaignId`: gate on `campaign.visibleAt === undefined || campaign.visibleAt <=
  Date.now()`, returning 404 otherwise — same shape as the existing `GET /characters/:characterId`
  handler.
- Update `createVisibilityDelayMs`'s own doc comment to mention Campaign alongside
  Character/Entity.

New Playwright spec `web/e2e/campaign-creation-lag.spec.ts`, directly mirroring
`character-creation-lag.spec.ts`'s two tests:
1. `createVisibilityDelayMs: 1600` (same reasoning as the Character version: long enough that the
   first three 750ms-interval polls miss and the fourth succeeds), create a Campaign from the
   Universe panel or Campaign picker, assert the workspace eventually renders the Campaign (not
   stuck loading) with no manual action.
2. `createVisibilityDelayMs: 999_999_999` (never becomes visible), create a Campaign, assert the
   `loadTimedOut` Retry/Back-to-Universe UI appears within `waitForCampaign`'s 15s timeout, and that
   clicking Retry (after the assertion, if desired) or Back-to-Universe navigates correctly.

## Out of Scope

Coordinating the multiplied poll count (BACKLOG's separate, explicitly-accepted item); any change to
`useUsers.ts`/`useCharacters.ts`/`useEntities.ts`'s public function signatures (refactor is internal
only).
