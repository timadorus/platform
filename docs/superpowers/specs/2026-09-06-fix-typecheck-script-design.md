# Fix the No-Op `npm run typecheck` Script

## Context

`docs/BACKLOG.md`'s Web SPA section documents that `web/package.json`'s `"typecheck": "vue-tsc
--noEmit"` runs against `web/tsconfig.json`, a solution-style config with `"files": []` — so it
checks zero files and exits 0 regardless of real type errors. The real type coverage has always
ridden along inside `npm run build`'s own `"build": "vue-tsc -b && vite build"` step. The BACKLOG
entry deferred fixing this because it could, in principle, surface a wave of pre-existing type
errors that had silently accumulated under the vacuous gate.

**Verified directly before writing this spec:** `npx vue-tsc -b --noEmit` (from `web/`) exits 0 with
zero output right now — there is no accumulated backlog of type errors to triage. `npm run build`'s
own `vue-tsc -b` step already keeps the codebase clean on every build; only the separate
`typecheck` script was vacuous. This means the originally-anticipated risk doesn't apply, and this
fix needs no separate "triage fallout" phase.

## Fix

Change `web/package.json`'s script from:

```json
"typecheck": "vue-tsc --noEmit",
```

to:

```json
"typecheck": "vue-tsc -b --noEmit",
```

matching `"build"`'s own invocation shape exactly (project-reference build mode, which is what
actually resolves `tsconfig.app.json`/`tsconfig.node.json` rather than the empty root
`tsconfig.json`).

## Testing

Run `npm run typecheck` before and after the change: before, confirm it exits 0 in well under a
second (proving it currently checks nothing — a quick timing sanity check, not a strict assertion);
after, confirm it also exits 0, but takes measurably longer (proving it now actually typechecks the
full `web/src` tree, not just that the exit code happens to match).

## Out of Scope

Any actual type-error fixes (none exist right now); CI workflow changes. Note: `.github/workflows/ci.yml`'s
`web-build` job runs `npm run typecheck` directly as its own dedicated step (before the `build` step),
and this fix converts that existing vacuous CI gate into a live one at approximately zero net CI cost —
the same `vue-tsc -b` work simply moves one step earlier and reuses its `.tsbuildinfo` cache in the
later `build` step. This branch also makes `npm run typecheck` meaningful for local pre-commit use.
