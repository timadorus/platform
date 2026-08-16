# Users UX Polish + Devcluster Seed Data Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the "created User doesn't show up" race in the web SPA with a wait-and-poll
"Creating User" modal, replace the Users page's dead self-link with a "Back to Main Page" link,
and seed a fresh `make dev-up` cluster with a User and a Ruleset through the platform's own HTTP
APIs.

**Architecture:** Three independent, additive changes on top of already-shipped code: a new
polling helper + modal in the Vue SPA's Users flow; a route-aware conditional in the shared
`AppHeader`; a new `SeedPlatformData` step in the Go devcluster tool, called once at the end of
`up` via a temporary Traefik port-forward (the same origin a developer's browser uses) and a
Zitadel `client_credentials` token (the same mechanism `printStatus` already documents for manual
"Direct API access").

**Tech Stack:** Vue 3 (`<script setup>`, TypeScript), `openapi-fetch` generated clients, Tailwind
utility classes, Vue Router; Go (`test/e2e/internal` / `test/e2e/cmd/devcluster`), `net/http`,
`kubectl port-forward`.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-08-16-users-ruleset-ux-and-seed-design.md`.
- No unit test framework exists in `web/` (no vitest/jest — confirmed: `package.json` has no test
  script, no `*.test.ts`/`*.spec.ts` files anywhere in `web/src`). Verification for web tasks is
  `npm run typecheck` (`vue-tsc --noEmit`), `npm run build`, and live browser checks — matching
  this app's existing convention, not a gap to fill in this plan.
- `test/e2e/internal` has no per-file unit tests for install/bootstrap helpers (confirmed:
  `zitadel.go`, `traefik.go`, `certmanager.go` etc. have no `_test.go` siblings — `state_test.go`
  is the one exception, for pure state-file logic). Verification for the devcluster task is
  `go build ./...`, `go vet ./...`, and a live `make dev-up` run — matching the existing
  convention for this package.
- `waitForUser`'s defaults: `intervalMs = 750`, `timeoutMs = 15000`.
- The seeded Ruleset's name is the literal string `"Timadorus"`, exported from `e2eutil` as
  `SeedRulesetName` (so both `seed.go` and `up.go`'s `printStatus` reference one constant instead
  of two copies of the literal).
- The seeded User's name is `zitadel.TestLoginName` (i.e. `"devuser@timadorus.local"`, derived at
  runtime from the same value the login form accepts) — never a separately hardcoded string.
- `SeedPlatformData` reuses the already-declared `devZitadelPort`/`devGatewayPort` constants in
  `up.go` transiently (same pattern `InstallZitadel` already uses for its own bootstrap
  port-forward) — no new port constants.
- A live kind cluster from this session's prior work is already running with the full stack
  installed (`cert-manager`, `cnpg-system`, `monitoring`, `nats`, `traefik`, `zitadel`,
  `timadorus-dev` namespaces all `Active`) — reuse it for live verification via `make dev-up`
  (idempotent, rebuilds/redeploys images unconditionally on every run — see `BuildTagLoadImages`
  in `test/e2e/internal/images.go`) rather than tearing it down first, unless a step's own
  verification requires a fresh cluster (none in this plan do).
- A working headless-Chromium Playwright setup already exists in this exact session's scratchpad
  at `/tmp/claude-1000/-home-sage-git-timadorus-platform/b10d1481-314d-4147-9528-858b4a844a4c/scratchpad`
  (`node_modules/playwright` installed, Chromium shared libs extracted to
  `debs/extracted/usr/lib/x86_64-linux-gnu`). Reuse it — do not reinstall Playwright or
  re-extract `.deb` packages. Launch pattern:
  ```bash
  cd /tmp/claude-1000/-home-sage-git-timadorus-platform/b10d1481-314d-4147-9528-858b4a844a4c/scratchpad
  LD_LIBRARY_PATH=debs/extracted/usr/lib/x86_64-linux-gnu node your-script.mjs
  ```

---

### Task 1: "Creating User" wait-and-poll flow

**Files:**
- Modify: `web/src/composables/useUsers.ts`
- Modify: `web/src/components/modals/CreateUserModal.vue`
- Create: `web/src/components/modals/CreatingUserModal.vue`
- Modify: `web/src/views/UsersAdminView.vue`

**Interfaces:**
- Produces: `useUsers().waitForUser(id: string, opts?: { intervalMs?: number; timeoutMs?: number }): Promise<boolean>` — polls `list()` until `id` appears in `users.value` (returns `true`) or `timeoutMs` elapses (returns `false`); never throws.
- Produces: `CreateUserModal` now emits `created: [id: string, name: string]` (was `created: []`).
- Produces: `CreatingUserModal` props `{ userId: string; userName: string }`, emits `done: []` (user confirmed visible) and `close: []` (dismissed, e.g. after a timeout or via `BaseModal`'s own ✕/click-outside).
- Consumes: `BaseModal.vue` (`title` prop, `close` emit, default slot), `BaseButton.vue` (`variant`, `type`, `disabled` props).

- [ ] **Step 1: Add `waitForUser` to `useUsers.ts`**

Replace the file's contents with:

```ts
import { ref } from 'vue'
import { getQueryClient, getCommandClient } from '@/api/client'
import { problemMessage } from '@/api/problem'

export interface UserSummary {
  id: string
  name: string
  isArchived: boolean
}

export function useUsers() {
  const users = ref<UserSummary[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function list() {
    loading.value = true
    error.value = null
    const { data, error: apiError } = await getQueryClient().GET('/users')
    loading.value = false
    if (apiError) {
      error.value = 'Failed to load Users.'
      return
    }
    users.value = (data ?? []) as UserSummary[]
  }

  async function create(name: string): Promise<string> {
    const { data, error: apiError } = await getCommandClient().POST('/users', { body: { name } })
    if (apiError || !data) {
      throw new Error(problemMessage(apiError) ?? 'Failed to create User.')
    }
    return data.id
  }

  async function rename(id: string, name: string): Promise<void> {
    const { error: apiError } = await getCommandClient().PATCH('/users/{userId}', {
      params: { path: { userId: id } },
      body: { name },
    })
    if (apiError) {
      throw new Error(problemMessage(apiError) ?? 'Failed to rename User.')
    }
  }

  async function archive(id: string): Promise<void> {
    const { error: apiError } = await getCommandClient().POST('/users/{userId}/archive', {
      params: { path: { userId: id } },
    })
    if (apiError) {
      throw new Error(problemMessage(apiError) ?? 'Failed to archive User.')
    }
  }

  // waitForUser polls list() until a User with id appears in the freshly fetched list, or
  // timeoutMs elapses. The Query API's read model is populated asynchronously by a projector, so
  // a User created via create() is not guaranteed to be present in the very next list() call.
  // Returns true once found, false on timeout — never throws, since a timeout is an expected,
  // handleable outcome for a caller (CreatingUserModal), not a programming error.
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

  return { users, loading, error, list, create, rename, archive, waitForUser }
}
```

- [ ] **Step 2: Typecheck**

Run: `cd web && npm run typecheck`
Expected: passes with no errors (this step only adds a function; nothing yet calls it with a
mismatched type).

- [ ] **Step 3: Change `CreateUserModal.vue`'s emit to carry the new id and name**

In `web/src/components/modals/CreateUserModal.vue`, change:

```ts
const emit = defineEmits<{ close: []; created: [] }>()
```
to:
```ts
const emit = defineEmits<{ close: []; created: [id: string, name: string] }>()
```

And change `submit()`'s body from:
```ts
  submitting.value = true
  error.value = null
  try {
    await create(name.value.trim())
    emit('created')
  } catch (err) {
```
to:
```ts
  submitting.value = true
  error.value = null
  try {
    const trimmed = name.value.trim()
    const id = await create(trimmed)
    emit('created', id, trimmed)
  } catch (err) {
```

- [ ] **Step 4: Create `CreatingUserModal.vue`**

```vue
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import BaseModal from '@/components/common/BaseModal.vue'
import BaseButton from '@/components/common/BaseButton.vue'
import { useUsers } from '@/composables/useUsers'

const props = defineProps<{ userId: string; userName: string }>()
const emit = defineEmits<{ done: []; close: [] }>()

const { waitForUser } = useUsers()
const timedOut = ref(false)

onMounted(async () => {
  const found = await waitForUser(props.userId)
  if (found) {
    emit('done')
  } else {
    timedOut.value = true
  }
})
</script>

<template>
  <BaseModal title="Creating User" @close="emit('close')">
    <p v-if="!timedOut" class="text-sm text-slate-600">Creating "{{ userName }}"…</p>
    <template v-else>
      <p class="mb-4 text-sm text-slate-600">
        Still waiting for "{{ userName }}" to appear — this is taking longer than expected.
      </p>
      <div class="flex justify-end">
        <BaseButton variant="secondary" @click="emit('close')">Close</BaseButton>
      </div>
    </template>
  </BaseModal>
</template>
```

- [ ] **Step 5: Wire the new flow into `UsersAdminView.vue`**

Replace the file's contents with:

```vue
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useUsers } from '@/composables/useUsers'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import CreateUserModal from '@/components/modals/CreateUserModal.vue'
import CreatingUserModal from '@/components/modals/CreatingUserModal.vue'
import AppHeader from '@/components/layout/AppHeader.vue'

const { users, list, rename, archive } = useUsers()
const error = ref<string | null>(null)
const showCreate = ref(false)
const creatingUser = ref<{ id: string; name: string } | null>(null)
const editingId = ref<string | null>(null)
const editingName = ref('')
const archiveTargetId = ref<string | null>(null)

onMounted(list)

function startEdit(id: string, currentName: string) {
  editingId.value = id
  editingName.value = currentName
}

async function saveEdit() {
  if (!editingId.value) return
  error.value = null
  try {
    await rename(editingId.value, editingName.value.trim())
    await list()
    editingId.value = null
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to rename.'
  }
}

async function confirmArchive() {
  if (!archiveTargetId.value) return
  error.value = null
  try {
    await archive(archiveTargetId.value)
    await list()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to archive.'
  } finally {
    archiveTargetId.value = null
  }
}

function onCreated(id: string, name: string) {
  showCreate.value = false
  creatingUser.value = { id, name }
}

async function onCreatingDone() {
  creatingUser.value = null
  await list()
}
</script>

<template>
  <AppHeader :universe-name="null" :campaign-name="null" />
  <div class="mx-auto max-w-xl p-6">
    <div class="mb-4 flex items-center justify-between">
      <h1 class="text-lg font-semibold text-slate-900">Users</h1>
      <BaseButton @click="showCreate = true">+ Create User</BaseButton>
    </div>
    <ErrorBanner :message="error" @dismiss="error = null" />
    <ul class="divide-y divide-slate-100 rounded-md border border-slate-200">
      <li v-for="user in users" :key="user.id" class="flex items-center justify-between gap-2 px-3 py-2">
        <template v-if="editingId === user.id">
          <input v-model="editingName" type="text" class="flex-1 rounded-md border border-slate-300 px-2 py-1 text-sm" />
          <button class="text-xs text-indigo-600 hover:underline" @click="saveEdit">Save</button>
          <button class="text-xs text-slate-400 hover:underline" @click="editingId = null">Cancel</button>
        </template>
        <template v-else>
          <span class="text-sm text-slate-900">{{ user.name }}</span>
          <span class="flex gap-3">
            <button class="text-xs text-indigo-600 hover:underline" @click="startEdit(user.id, user.name)">Rename</button>
            <button class="text-xs text-red-500 hover:underline" @click="archiveTargetId = user.id">Archive</button>
          </span>
        </template>
      </li>
      <li v-if="users.length === 0" class="px-3 py-2 text-xs text-slate-400">No Users yet.</li>
    </ul>

    <CreateUserModal v-if="showCreate" @close="showCreate = false" @created="onCreated" />
    <CreatingUserModal
      v-if="creatingUser"
      :user-id="creatingUser.id"
      :user-name="creatingUser.name"
      @done="onCreatingDone"
      @close="creatingUser = null"
    />
    <ConfirmDialog
      v-if="archiveTargetId"
      title="Archive User"
      message="This user will be hidden from pickers and lists. This cannot be undone through this app."
      confirm-label="Archive"
      @confirm="confirmArchive"
      @cancel="archiveTargetId = null"
    />
  </div>
</template>
```

- [ ] **Step 6: Typecheck and build**

Run: `cd web && npm run typecheck && npm run build`
Expected: both succeed with no errors.

- [ ] **Step 7: Commit**

```bash
git add web/src/composables/useUsers.ts web/src/components/modals/CreateUserModal.vue \
  web/src/components/modals/CreatingUserModal.vue web/src/views/UsersAdminView.vue
git commit -m "web: show a Creating User modal until the new user is visible

CreateUserModal's create() returned an id the query-api's read model
hadn't necessarily caught up to yet, so UsersAdminView's single
post-create list() refresh could silently omit the just-created user.
useUsers now exposes waitForUser(id), polled by a new CreatingUserModal
shown between create and the final list refresh."
```

- [ ] **Step 8: Live verification (headless Chromium)**

Rebuild and redeploy the web-ui image onto the already-running dev cluster (idempotent, safe to
run against a live cluster — see Global Constraints):

```bash
make dev-up
```

Port-forward Traefik in the background, then drive a real browser through create-a-user:

```bash
kubectl port-forward --namespace traefik svc/traefik 8080:80 >/tmp/pf-traefik-task1.log 2>&1 &
```

Write and run (adjust the login step to match whatever auth state is already valid in this
session — reuse the credentials `make dev-up` just printed if a fresh login is needed):

```js
// verify-creating-modal.mjs
import { chromium } from 'playwright';
const browser = await chromium.launch();
const page = await browser.newPage();
await page.goto('http://localhost:8080/users');
// ... log in if redirected, then:
await page.click('text=+ Create User');
await page.fill('input[type=text]', 'playwright-verify-user');
await page.click('text=Create');
await page.screenshot({ path: 'creating-modal.png' });
await page.waitForSelector('text=Creating User', { state: 'detached', timeout: 20000 });
await page.screenshot({ path: 'after-creating-modal.png' });
await browser.close();
```

Expected: `creating-modal.png` shows the "Creating User" modal with the entered name;
`after-creating-modal.png` shows the modal gone and the new user present in the list with no
manual refresh. View both screenshots to confirm.

---

### Task 2: AppHeader — "Back to Main Page" on the Users page

**Files:**
- Modify: `web/src/components/layout/AppHeader.vue`

**Interfaces:**
- Consumes: `useRoute()` from `vue-router` (already a project dependency), matching `route.name === 'users-admin'` against the route name declared in `web/src/router/index.ts:55`.

- [ ] **Step 1: Make the icon route-aware**

Replace `web/src/components/layout/AppHeader.vue`'s contents with:

```vue
<script setup lang="ts">
import { useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

defineProps<{
  universeName: string | null
  campaignName: string | null
}>()
const emit = defineEmits<{
  'click-universe-badge': []
  'click-campaign-badge': []
}>()

const auth = useAuthStore()
const route = useRoute()
</script>

<template>
  <header class="flex h-14 flex-shrink-0 items-center gap-3 border-b border-slate-200 bg-white px-4">
    <span class="text-sm font-semibold text-slate-900">Timadorus</span>
    <button
      v-if="universeName"
      class="rounded-full bg-slate-100 px-3 py-1 text-xs text-slate-700 hover:bg-slate-200"
      @click="emit('click-universe-badge')"
    >
      🌍 {{ universeName }}
    </button>
    <button
      v-if="campaignName"
      class="rounded-full bg-slate-100 px-3 py-1 text-xs text-slate-700 hover:bg-slate-200"
      @click="emit('click-campaign-badge')"
    >
      🎲 {{ campaignName }}
    </button>
    <div class="ml-auto flex items-center gap-2">
      <span class="text-sm text-slate-700">{{ auth.displayName }}</span>
      <router-link
        v-if="route.name === 'users-admin'"
        to="/"
        class="text-slate-400 hover:text-slate-700"
        title="Back to Main Page"
        aria-label="Back to Main Page"
      >
        🏠
      </router-link>
      <router-link
        v-else
        to="/users"
        class="text-slate-400 hover:text-slate-700"
        title="Manage Users"
        aria-label="Manage Users"
      >
        ⚙
      </router-link>
      <button class="text-xs text-slate-400 hover:text-slate-700" @click="auth.logout()">Log out</button>
    </div>
  </header>
</template>
```

- [ ] **Step 2: Typecheck and build**

Run: `cd web && npm run typecheck && npm run build`
Expected: both succeed with no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/layout/AppHeader.vue
git commit -m "web: AppHeader shows Back to Main Page instead of Manage Users on /users

The gear link to /users was a dead link to itself while already on that
page, with no way back except the browser's own back button. AppHeader
is now route-aware: on users-admin it shows a home icon linking to /,
everywhere else it's unchanged."
```

- [ ] **Step 4: Live verification (headless Chromium)**

Reuse the same port-forward from Task 1 Step 8 if still running (otherwise start it as in that
step). This can share the same `make dev-up` run as Task 1 — no separate rebuild needed if Task 1
already ran it after this task's edit; otherwise run `make dev-up` again first.

```js
// verify-header-icon.mjs
import { chromium } from 'playwright';
const browser = await chromium.launch();
const page = await browser.newPage();
await page.goto('http://localhost:8080/users');
// ... log in if redirected
await page.screenshot({ path: 'users-header.png' });
const backLink = await page.$('a[aria-label="Back to Main Page"]');
console.log('back link present:', !!backLink);
await backLink.click();
console.log('url after click:', page.url());
await browser.close();
```

Expected: `users-header.png` shows the 🏠 icon (not ⚙) in the header while on `/users`; the
script logs `back link present: true` and a post-click URL of `http://localhost:8080/`. Also
spot-check (via the same session, navigating to `/`) that the ⚙ "Manage Users" icon still appears
and still links to `/users` on the non-Users pages.

---

### Task 3: Devcluster — seed a User and a Ruleset

**Files:**
- Create: `test/e2e/internal/seed.go`
- Modify: `test/e2e/cmd/devcluster/up.go`

**Interfaces:**
- Consumes: `e2eutil.ZitadelBootstrap` (`Authority`, `APIClientID`, `APIClientSecret`,
  `TestLoginName`), `e2eutil.StartPortForward(service string, localPort, servicePort int, healthPath string, timeout time.Duration) (*e2eutil.PortForward, error)`, `e2eutil.Namespace` (package var), `e2eutil.TraefikNamespace`, `e2eutil.ZitadelNamespace`, `e2eutil.ZitadelServiceName` — all already defined in `test/e2e/internal`.
- Produces: `e2eutil.SeedRulesetName` (exported const, `"Timadorus"`), `e2eutil.SeedPlatformData(zitadel e2eutil.ZitadelBootstrap, zitadelPort, gatewayPort int) error`.

- [ ] **Step 1: Create `test/e2e/internal/seed.go`**

```go
package e2eutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SeedRulesetName is the fixed Ruleset name devcluster seeds into a fresh platform — a literal
// per this tool's own requirement, exported so up.go's printStatus can reference the same
// constant seed.go seeds with instead of duplicating the string.
const SeedRulesetName = "Timadorus"

// seedResource is the minimal shape shared by every query-api list-of-summaries response
// (/users, /rulesets, ...) — id/name, same fields the web SPA's own UserSummary/RulesetSummary
// types carry.
type seedResource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SeedPlatformData creates baseline dev data through the platform's own HTTP APIs, reached via a
// temporary port-forward to the same shared Traefik Gateway a developer's browser uses (a live
// smoke test of that routing as a side effect): a User matching zitadel.TestLoginName (i.e.
// "devuser@timadorus.local" — derived from the same value the login form accepts, not
// re-hardcoded, so it can never drift from the account a developer actually logs in with) and a
// Ruleset named SeedRulesetName. Each is checked against the query-api's own list by name first
// and skipped if already present — User/Ruleset names carry no uniqueness constraint at the
// domain level, so an unconditional create would pile up duplicates on every repeated
// `make dev-up` against an already-seeded cluster. zitadelPort/gatewayPort are reused transiently
// (matching InstallZitadel's own reuse of externalPort for its bootstrap port-forward) — nothing
// else holds either port open during `up` itself.
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
	if err := ensureSeedResource(base, token, "/api/query/rulesets", "/api/command/rulesets", SeedRulesetName); err != nil {
		return fmt.Errorf("e2eutil: seed ruleset: %w", err)
	}
	return nil
}

// fetchSeedAccessToken fetches a client_credentials access token for the FirstInstance-bootstrapped
// API machine user, via a temporary port-forward to Zitadel's own Service — the identical request
// printStatus's own "Direct API access" curl example documents as working.
func fetchSeedAccessToken(zitadel ZitadelBootstrap, zitadelPort int) (string, error) {
	prevNamespace := Namespace
	Namespace = ZitadelNamespace
	pf, err := StartPortForward(ZitadelServiceName, zitadelPort, 8080, "/.well-known/openid-configuration", 2*time.Minute)
	Namespace = prevNamespace
	if err != nil {
		return "", fmt.Errorf("port-forward zitadel: %w", err)
	}
	defer pf.Stop()

	form := url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {"openid profile"},
	}
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/oauth/v2/token", zitadelPort),
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(zitadel.APIClientID, zitadel.APIClientSecret)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token: HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("token response had no access_token: %s", string(body))
	}
	return out.AccessToken, nil
}

// ensureSeedResource GETs listPath (bearer-authenticated) and, unless an entry named name
// already exists, POSTs {"name": name} to createPath.
func ensureSeedResource(base, token, listPath, createPath, name string) error {
	exists, err := seedNameExists(base, token, listPath, name)
	if err != nil {
		return fmt.Errorf("check existing: %w", err)
	}
	if exists {
		return nil
	}

	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return fmt.Errorf("marshal create request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, base+createPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("create %q: %w", name, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read create response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("create %q: HTTP %d: %s", name, resp.StatusCode, string(respBody))
	}
	return nil
}

func seedNameExists(base, token, listPath, name string) (bool, error) {
	req, err := http.NewRequest(http.MethodGet, base+listPath, nil)
	if err != nil {
		return false, fmt.Errorf("build list request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("list: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("read list response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("list: HTTP %d: %s", resp.StatusCode, string(body))
	}
	var items []seedResource
	if err := json.Unmarshal(body, &items); err != nil {
		return false, fmt.Errorf("decode list response: %w", err)
	}
	for _, item := range items {
		if item.Name == name {
			return true, nil
		}
	}
	return false, nil
}
```

- [ ] **Step 2: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: both clean.

- [ ] **Step 3: Wire `SeedPlatformData` into `up.go`, after `InstallPlatform` succeeds**

In `test/e2e/cmd/devcluster/up.go`, change:

```go
	}); err != nil {
		return fmt.Errorf("install platform: %w", err)
	}

	printStatus(zitadel)
	return nil
}
```
to:
```go
	}); err != nil {
		return fmt.Errorf("install platform: %w", err)
	}

	if err := e2eutil.SeedPlatformData(zitadel, devZitadelPort, devGatewayPort); err != nil {
		return fmt.Errorf("seed platform data: %w", err)
	}

	printStatus(zitadel)
	return nil
}
```

- [ ] **Step 4: Print the seeded data in `printStatus`**

In the same file, change:
```go
	fmt.Println("Log in (another terminal — Zitadel needs its own two port-forwards, see below) with:")
	fmt.Printf("  username: %s\n", zitadel.TestLoginName)
	fmt.Printf("  password: %s\n\n", zitadel.TestPassword)
	fmt.Println("Zitadel (needed for the login redirect above to resolve):")
```
to:
```go
	fmt.Println("Log in (another terminal — Zitadel needs its own two port-forwards, see below) with:")
	fmt.Printf("  username: %s\n", zitadel.TestLoginName)
	fmt.Printf("  password: %s\n\n", zitadel.TestPassword)
	fmt.Printf("Pre-seeded: User %q, Ruleset %q.\n\n", zitadel.TestLoginName, e2eutil.SeedRulesetName)
	fmt.Println("Zitadel (needed for the login redirect above to resolve):")
```

- [ ] **Step 5: Build and vet again**

Run: `go build ./... && go vet ./...`
Expected: both clean.

- [ ] **Step 6: Commit**

```bash
git add test/e2e/internal/seed.go test/e2e/cmd/devcluster/up.go
git commit -m "test/e2e/devcluster: seed a User and a Ruleset on dev-up

A fresh dev-up gave a developer a working Zitadel login into a
completely empty platform (no Users, no Rulesets). SeedPlatformData now
runs after the platform install succeeds: fetches a client_credentials
token from Zitadel (the same mechanism printStatus's own 'Direct API
access' example already documents), then through a temporary port-forward
to Traefik (the same origin a browser uses) creates a User matching the
Zitadel test login and a Ruleset named \"Timadorus\" — checking each
query-api list by name first so repeated dev-up runs don't pile up
duplicates."
```

- [ ] **Step 7: Live verification**

Run against the already-live cluster (idempotent — safe to rerun):

```bash
make dev-up
```

Expected in the output: a new `Pre-seeded: User "devuser@timadorus.local", Ruleset "Timadorus".`
line.

Confirm both actually exist, using the exact "Direct API access" pattern `dev-up` itself just
printed (fill in the printed `APIClientID`/`APIClientSecret` and ports):

```bash
TOKEN=$(curl -s -u <APIClientID>:<APIClientSecret> -d grant_type=client_credentials -d "scope=openid profile" http://localhost:8084/oauth/v2/token | jq -r .access_token)
curl -s http://localhost:8080/api/query/users -H "Authorization: Bearer $TOKEN" | jq .
curl -s http://localhost:8080/api/query/rulesets -H "Authorization: Bearer $TOKEN" | jq .
```

(Needs `kubectl port-forward --namespace zitadel svc/zitadel 8084:8080` and
`kubectl port-forward --namespace traefik svc/traefik 8080:80` running in other terminals — both
already printed by `dev-up`.)

Expected: the users list contains exactly one entry named `devuser@timadorus.local`, the rulesets
list contains exactly one entry named `Timadorus`.

Then run `make dev-up` a second time and repeat both `curl` checks: expected counts are still
exactly one each (no duplicates from the idempotency check).

---

## Final Verification

- `cd web && npm run typecheck && npm run build` clean.
- `go build ./... && go vet ./...` clean (from repo root).
- `make dev-up` against the live cluster prints the pre-seeded line and both Users/Rulesets
  queries confirm exactly one seeded row each, unchanged across a second `make dev-up` run.
- Live browser: creating a User from `/users` shows the "Creating User" modal and it clears on
  its own with the new user visible in the list; `/users` shows a "Back to Main Page" 🏠 link that
  navigates to `/`, and every other view still shows the "Manage Users" ⚙ link to `/users`.
