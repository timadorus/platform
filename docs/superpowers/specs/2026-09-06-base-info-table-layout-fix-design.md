# Character Base Info Table — Column-Width Jitter Fix

## Context

`docs/BACKLOG.md`'s Web SPA section documents cosmetic column-width jitter in
`BaseInfoTable.vue`'s table (the Character detail page's "Base Info" card). The table
(`web/src/components/character/BaseInfoTable.vue:120`) has no `<colgroup>` and uses the browser
default `table-layout: auto`, which re-measures column widths from the content of every currently
rendered row. Two rows in this table are conditionally rendered (`v-if="editingPlayer"` inserts a
`<UserPicker>` row; the Traits row's third column grows when `showAddTrait` opens a `<select>`), so
toggling either state changes what content participates in the auto-layout's width calculation —
shifting column widths and causing "Archive Character" (last row, 3rd column) and "Character Name"
(1st row, 1st column) to wrap differently depending on which other row is currently expanded.

## Fix

Switch the table to `table-layout: fixed` with an explicit `<colgroup>`, so column widths come only
from the `<colgroup>` and never depend on which optional row is currently rendered:

```html
<table class="w-full table-fixed text-sm">
  <colgroup>
    <col class="w-1/4" />
    <col />
    <col class="w-44" />
  </colgroup>
  <tbody>
    ...
  </tbody>
</table>
```

Column 1 (labels: "Character Name", "Player", "Traits") gets a fixed quarter-width. Column 3
(actions: Rename/Cancel, Reassign Player/Cancel, Archive Character) gets a fixed `w-44` (11rem)
— wide enough for "Archive Character" (the widest single action label) to stay on one line at the
viewport width the BACKLOG entry names (~1400px) and reasonably narrower viewports. Column 2 (values)
takes the remaining space via the implicit `<col />` with no explicit width.

**`w-44` is a starting value, not verified pixel-exact against the real rendered font** — if the
implementer's own visual/measurement check (below) shows "Archive Character" still wrapping at this
width, widen the third column's class (e.g. `w-48` or `w-52`) until it doesn't, and note the actual
value used and why in the task report. Any width in this range is a purely cosmetic choice with no
other consequence.

## Testing

New Playwright test in `web/e2e/character-attributes.spec.ts` or a new small spec file (implementer's
choice — check whether an existing spec already mounts a Character detail page in a state where
opening the Reassign Player picker is easy to trigger, and extend that file if so, to avoid a 5th
near-duplicate `seedState`/`seedAuth` boilerplate block) that:
1. Navigates to a Character detail page.
2. Records `page.getByRole('cell', { name: 'Archive Character' }).boundingBox()` — or, more directly,
   the bounding box of the "Character Name" label cell (`page.locator('td', { hasText: 'Character Name' })`)
   — in the default (no picker open) state.
3. Opens the Reassign Player picker (click "Reassign Player").
4. Re-measures the same cell's `boundingBox()` and asserts the `width` is unchanged (exact equality —
   with `table-layout: fixed` there is no reason for it to differ at all, so no tolerance is needed).
5. Also asserts the "Archive Character" button's bounding box `height` stays at a single-line height
   (e.g. `< 28` — pick the concrete threshold from the actual rendered height you observe, document
   it) in both states, directly proving the wrap this BACKLOG entry reports no longer happens.

## Out of Scope

Any other table in the SPA (this is a single, isolated component); the underlying design spec's
choice of a `<table>` markup for this card at all (BACKLOG explicitly notes the design spec
prescribed this exact table markup — not being revisited here).
