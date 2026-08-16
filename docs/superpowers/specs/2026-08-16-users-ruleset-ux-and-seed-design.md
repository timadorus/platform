# Users UX Polish + Devcluster Seed Data — Design Spec

## Context

Two independent, small pieces of follow-up work on top of the already-merged web SPA and
`make dev-up` tool:

1. The web SPA (`web/`, Vue 3 + `oapi-codegen`/`openapi-fetch` clients against the CQRS
   command/query APIs) has two rough edges in its Users flow:
   - Creating a User calls the command API, which returns immediately with a generated id, but
     the read model that backs the Users list is populated asynchronously by a projector. The
     current `UsersAdminView` closes the create modal and re-fetches the list exactly once, so
     the new user is frequently missing from the list the user sees right after creating it.
   - `AppHeader` (shared by all 4 views) always shows a ⚙ "Manage Users" link to `/users` — even
     while already on the Users page, where it's a dead link to itself and there's no way back
     to the rest of the app from there except the browser's own back button.
2. `test/e2e/cmd/devcluster` (`make dev-up`) already provisions a real local Zitadel IdP with one
   login-capable human account (`devuser@timadorus.local`), but the platform's own domain data
   (Postgres event store, via the command/query APIs) starts completely empty — a fresh cluster
   gives a developer a working login into an empty app.

## 1. Create User: wait for it to appear

**Fix:** `useUsers.ts` gains a polling helper; `CreateUserModal` threads the created id back to
its parent; a new modal shows while the parent waits for the id to appear in the list.

```ts
// web/src/composables/useUsers.ts
async function waitForUser(
  id: string,
  opts: { intervalMs?: number; timeoutMs?: number } = {},
): Promise<boolean> {
  const intervalMs = opts.intervalMs ?? 750
  const timeoutMs = opts.timeoutMs ?? 15000
  const deadline = Date.now() + timeoutMs
  for (;;) {
    await list()
    if (users.value.some((u) => u.id === id)) return true
    if (Date.now() >= deadline) return false
    await new Promise((resolve) => setTimeout(resolve, intervalMs))
  }
}
```

- `CreateUserModal.vue`: `defineEmits<{ close: []; created: [id: string, name: string] }>()`,
  emits both values on success (mirrors `CreateUniverseModal`'s existing `created: [id: string]`
  pattern).
- New `web/src/components/modals/CreatingUserModal.vue`: props `userId: string`, `userName:
  string`; emits `done: []` (this user is now visible) and `close: []` (user dismissed early via
  `BaseModal`'s existing ✕/click-outside). On mount, calls `waitForUser(userId)`; on success emits
  `done`; on timeout shows an inline "Still waiting for '<userName>' to appear — this is taking
  longer than expected." message with a `BaseButton` "Close" (wired to the same `close` emit,
  no new dismiss mechanism needed). Visual style matches the rest of the app: plain text inside
  `BaseModal`, no spinner asset (none exists in this codebase; other loading states here are
  plain "Loading…" text).
- `UsersAdminView.vue`: replaces the single `showCreate` boolean's `onCreated()` handler with:
  `showCreate` (form modal) and `creatingUser: { id: string; name: string } | null` (waiting
  modal). `CreateUserModal`'s `created` handler sets `showCreate = false` and
  `creatingUser = { id, name }`. `CreatingUserModal`'s `done` handler calls `list()` (guaranteed
  to now include the new user) and clears `creatingUser`; its `close` handler just clears
  `creatingUser` without forcing a `list()` call.

## 2. AppHeader: "Manage Users" vs "Back to Main Page"

**Fix:** `AppHeader.vue` calls `useRoute()` from `vue-router` and conditionally renders one of
two `router-link`s in the same slot the ⚙ occupies today:

- `route.name === 'users-admin'` → 🏠 linking to `/`, `title`/`aria-label` "Back to Main Page".
- otherwise (unchanged) → ⚙ linking to `/users`, `title`/`aria-label` "Manage Users".

No other view changes — the gear remains the only entry point into `/users` from the 3 other
views (`WorkspaceView`, `UniversePickerView`, `CampaignPickerView`), and `/` (the
`universe-picker` route) is "the main page."

## 3 & 4. Devcluster: seed a User and a Ruleset

**Fix:** a new `SeedPlatformData` step in `test/e2e/internal` (new file `seed.go`), called from
`up.go` right after `InstallPlatform` succeeds, unconditionally (idempotent by design, so safe to
run on every `up`, matching the unconditional-but-idempotent shape of e.g.
`WaitForCertManagerWebhook`).

```go
// test/e2e/internal/seed.go (new file)

// devSeedRulesetName is the fixed Ruleset name devcluster seeds — literal per requirement.
const devSeedRulesetName = "Timadorus"

// SeedPlatformData creates baseline dev data through the platform's own HTTP APIs, reached via
// the same shared Traefik Gateway a developer's browser uses (a live smoke test of that routing
// as a side effect): a User matching zitadel.TestLoginName (i.e. "devuser@timadorus.local" —
// derived from the same value the login form accepts, not re-hardcoded, so it can never drift
// from the account a developer actually logs in with) and a Ruleset named "Timadorus". Checks
// each query-api list by name first and skips creation if already present — User/Ruleset names
// carry no uniqueness constraint at the domain level, so an unconditional create would pile up
// duplicates on every repeated `make dev-up` against an already-seeded cluster.
func SeedPlatformData(zitadel ZitadelBootstrap, zitadelPort, gatewayPort int) error {
	token, err := fetchSeedAccessToken(zitadel, zitadelPort)
	if err != nil {
		return fmt.Errorf("e2eutil: fetch seed access token: %w", err)
	}

	prevNamespace := Namespace
	Namespace = TraefikNamespace
	pf, err := StartPortForward("traefik", gatewayPort, 80, "/", 2*time.Minute)
	Namespace = prevNamespace
	if err != nil {
		return fmt.Errorf("e2eutil: port-forward traefik for seeding: %w", err)
	}
	defer pf.Stop()

	base := fmt.Sprintf("http://127.0.0.1:%d", gatewayPort)
	if err := ensureSeedResource(base, token, "/api/query/users", "/api/command/users", zitadel.TestLoginName); err != nil {
		return fmt.Errorf("e2eutil: seed user: %w", err)
	}
	if err := ensureSeedResource(base, token, "/api/query/rulesets", "/api/command/rulesets", devSeedRulesetName); err != nil {
		return fmt.Errorf("e2eutil: seed ruleset: %w", err)
	}
	return nil
}
```

- `fetchSeedAccessToken(zitadel, zitadelPort)`: temporarily port-forwards Zitadel's Service
  (`Namespace = ZitadelNamespace`, same swap-and-restore pattern `InstallZitadel` already uses),
  then does the exact `client_credentials` request `printStatus`'s own "Direct API access" curl
  example documents (`POST /oauth/v2/token`, Basic auth `zitadel.APIClientID`/`APIClientSecret`,
  `grant_type=client_credentials`, `scope=openid profile`), and returns the decoded
  `access_token`.
- `ensureSeedResource(base, token, listPath, createPath, name)`: `GET base+listPath` with the
  bearer token, decodes a `[]struct{ID, Name string; ...}` JSON array, returns immediately if any
  entry's `Name == name`; otherwise `POST base+createPath` with `{"name": name}` and expects
  `201`.
- `up.go`: after the existing `InstallPlatform(...)` call succeeds,
  `if err := e2eutil.SeedPlatformData(zitadel, devZitadelPort, devGatewayPort); err != nil { return fmt.Errorf("seed platform data: %w", err) }`
  — reuses the already-declared `devZitadelPort`/`devGatewayPort` constants (transiently, exactly
  as `InstallZitadel` already reuses `externalPort` for its own bootstrap port-forward; nothing
  else holds either port open during `up` itself — the human's own long-lived port-forwards from
  `printStatus` don't start until after `up` returns).
- `printStatus`: one added line after the existing username/password block —
  `Pre-seeded: User "<zitadel.TestLoginName>", Ruleset "Timadorus".`

### Out of scope

- No dev-down changes: seeded data lives in the platform's own Postgres event store, torn down
  along with everything else `dev-down` already removes.
- No uniqueness enforcement at the domain/API level — `ensureSeedResource`'s name-based check is
  devcluster-tooling-only idempotency, not a product feature.

## Verification

- `go build ./...`, `go vet ./...` clean (devcluster changes).
- `cd web && npm run build` (or `vue-tsc --noEmit`) clean (SPA changes).
- Live: fresh `make dev-up` on a torn-down cluster — confirm the printed pre-seeded line, confirm
  `GET /api/query/users` and `/rulesets` (via the printed curl pattern) show the seeded rows, then
  a second `make dev-up` on the same cluster confirms no duplicates appear.
- Live, in a browser (reusing the Playwright setup already proven working in this environment):
  create a User via the UI, confirm the "Creating…" modal appears and clears on its own once the
  user shows up in the list (no manual refresh); visit `/users` and confirm the header now shows
  a "Back to Main Page" 🏠 link instead of the gear, and confirm it navigates to `/`.
