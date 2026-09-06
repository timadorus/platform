# Fix No-Op typecheck Script Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `npm run typecheck` actually typecheck `web/src`, instead of vacuously checking zero
files against a solution-style `tsconfig.json`.

**Architecture:** One-line `package.json` script change. Already verified (see the design spec) that
this surfaces zero pre-existing type errors — `npm run build`'s own `vue-tsc -b` step already keeps
the codebase clean, so no fallout-triage work is needed.

**Tech Stack:** `vue-tsc`, npm scripts.

## Global Constraints

- Only `web/package.json` changes.
- `npm run typecheck` must exit 0 after the change (already confirmed true — this is a regression
  check, not new work).

---

### Task 1: Fix the script

**Files:**
- Modify: `web/package.json`

- [ ] **Step 1: Time the current (vacuous) script**

Run: `cd web && time npm run typecheck`
Expected: exits 0, completes in well under a second — confirms it currently checks nothing.

- [ ] **Step 2: Change the script**

In `web/package.json`, change:

```json
    "typecheck": "vue-tsc --noEmit",
```

to:

```json
    "typecheck": "vue-tsc -b --noEmit",
```

- [ ] **Step 3: Time the fixed script**

Run: `cd web && time npm run typecheck`
Expected: exits 0, takes measurably longer than Step 1 (proving it now actually typechecks the
`web/src` tree via `tsconfig.app.json`/`tsconfig.node.json`, not just that the exit code happens to
match).

- [ ] **Step 4: Run the full build and e2e suite as a final sanity check**

Run: `cd web && npm run build && npm run test:e2e`
Expected: build clean, all e2e tests green — unaffected by this change (a separate script).

- [ ] **Step 5: Commit**

```bash
git add web/package.json
git commit -m "web: fix npm run typecheck to actually typecheck web/src"
```
