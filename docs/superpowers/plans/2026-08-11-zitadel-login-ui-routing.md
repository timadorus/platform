# Zitadel Login UI Routing Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `make dev-up`'s printed browser login flow actually work — give Zitadel's
separate login-UI Service (`zitadel-login`, port 3000, currently never exposed) its own
port-forward, and tell Zitadel's backend where to find it.

**Architecture:** One new local port (`devZitadelLoginPort`), one new `--set` pair on the
already-existing Zitadel Helm install (`zitadel.configmapConfig.DefaultInstance.Features.LoginV2.{BaseURI,Required}`),
one new printed port-forward line. No new Kubernetes objects.

**Tech Stack:** Unchanged (Go, Helm `--set` flags, `kubectl port-forward`) — plus, for this
plan's own live verification only, a real headless Chromium driven via `playwright` (Node),
already proven working in this environment (system libs extracted from `.deb` packages without
root, `LD_LIBRARY_PATH` pointed at them).

**Design spec:** `docs/superpowers/specs/2026-08-11-zitadel-login-ui-routing-design.md` — read
it once for full rationale.

## Global Constraints

- No new Kubernetes objects (Gateway/HTTPRoute) — the chosen fix is Zitadel's own documented
  separate-origin pattern, not path-based routing.
- The exact `--set` key path must use the `zitadel.configmapConfig.*` prefix (confirmed against
  the actual merged code, not the unprefixed form an earlier draft assumed) — `zitadel.go`'s
  existing `ExternalDomain`/`FirstInstance.*` flags are the ground truth for this prefix.
- `LoginV2.BaseURI`'s exact trailing-slash format is genuinely unconfirmed from research alone
  (sources disagreed) — this plan's live verification step is where it gets settled, not a
  guess baked in without checking.
- Live verification for this plan must use a **real browser** (Playwright/Chromium), not a
  manual curl simulation — that's the whole reason this fix exists.

## File Structure Overview

```
test/e2e/internal/zitadel.go         # installZitadelHelmRelease/InstallZitadel: + loginPort param, 2 new --set flags
test/e2e/cmd/devcluster/up.go        # + devZitadelLoginPort const, call-site update, printStatus: 3rd port-forward line
README.md                             # dual-port-forward paragraph -> triple
```

## Task List

1. Wire the login-UI port-forward + BaseURI config, verify live with a real browser

---

### Task 1: Login-UI port-forward + BaseURI config, real-browser verification

**Files:**
- Modify: `test/e2e/internal/zitadel.go`
- Modify: `test/e2e/cmd/devcluster/up.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: existing `ZitadelServiceName`, `ZitadelNamespace` consts (already exported).
- Produces: `installZitadelHelmRelease(externalPort int, loginPort int, humanUsername,
  humanPassword string) error` and `InstallZitadel(externalPort int, loginPort int,
  spaRedirectURI, spaPostLogoutURI string) (ZitadelBootstrap, error)` — both gain one new
  parameter; `up.go`'s one call site updates to match.

- [ ] **Step 1: Modify `test/e2e/internal/zitadel.go`** — `installZitadelHelmRelease`'s
  signature and `--set` flags

Change:
```go
func installZitadelHelmRelease(externalPort int, humanUsername, humanPassword string) error {
```
to:
```go
// loginPort must match whatever local port devcluster port-forwards Zitadel's separate
// "zitadel-login" Service (port 3000) to — the chart deploys the login UI as its own
// Deployment/Service, and Zitadel's backend needs to be told its externally-reachable address
// via Features.LoginV2.BaseURI (its own setup job otherwise defaults this to the SAME origin
// as the backend itself, assuming a reverse proxy routes /ui/v2/login there — nothing in this
// devcluster setup does, so without this the backend's own OIDC-authorize redirect leads to a
// 404 on itself). Confirmed live: the relevant upstream bug
// (zitadel/zitadel#10405, "setting this env var has no effect") is closed/fixed well before
// this repo's pinned zitadelChartVersion's Zitadel version (v4.15.3 vs. the bug's v4.0.0).
func installZitadelHelmRelease(externalPort, loginPort int, humanUsername, humanPassword string) error {
```

Change the `args` slice — add two lines right after the existing `ExternalSecure`/`TLS.Enabled`
flags (exact insertion point: immediately after `"--set",
"zitadel.configmapConfig.TLS.Enabled=false",` and before the `Org.Human` block):
```go
		"--set", "zitadel.configmapConfig.ExternalDomain=localhost",
		"--set", fmt.Sprintf("zitadel.configmapConfig.ExternalPort=%d", externalPort),
		"--set", "zitadel.configmapConfig.ExternalSecure=false",
		"--set", "zitadel.configmapConfig.TLS.Enabled=false",
```
to:
```go
		"--set", "zitadel.configmapConfig.ExternalDomain=localhost",
		"--set", fmt.Sprintf("zitadel.configmapConfig.ExternalPort=%d", externalPort),
		"--set", "zitadel.configmapConfig.ExternalSecure=false",
		"--set", "zitadel.configmapConfig.TLS.Enabled=false",
		"--set", fmt.Sprintf("zitadel.configmapConfig.DefaultInstance.Features.LoginV2.BaseURI=http://localhost:%d/ui/v2/login", loginPort),
		"--set", "zitadel.configmapConfig.DefaultInstance.Features.LoginV2.Required=true",
```
(The exact `BaseURI` string's trailing-slash format is unconfirmed from research alone — this
step's own Step 5 live check is where that gets settled. If the login redirect 404s again with
this value, try appending a trailing `/` and re-verify before looking anywhere else.)

- [ ] **Step 2: Modify `test/e2e/internal/zitadel.go`** — `InstallZitadel`'s signature

Change:
```go
func InstallZitadel(externalPort int, spaRedirectURI, spaPostLogoutURI string) (ZitadelBootstrap, error) {
	humanUsername := "devuser"
	humanPassword, err := randomPassword()
	if err != nil {
		return ZitadelBootstrap{}, err
	}

	if err := installZitadelHelmRelease(externalPort, humanUsername, humanPassword); err != nil {
		return ZitadelBootstrap{}, err
	}
```
to:
```go
func InstallZitadel(externalPort, loginPort int, spaRedirectURI, spaPostLogoutURI string) (ZitadelBootstrap, error) {
	humanUsername := "devuser"
	humanPassword, err := randomPassword()
	if err != nil {
		return ZitadelBootstrap{}, err
	}

	if err := installZitadelHelmRelease(externalPort, loginPort, humanUsername, humanPassword); err != nil {
		return ZitadelBootstrap{}, err
	}
```

- [ ] **Step 3: Modify `test/e2e/cmd/devcluster/up.go`**

Add the new constant:
```go
	// devZitadelPort is Zitadel's own local port-forward — Zitadel can't be reverse-proxied
	// under devGatewayPort's shared host (design spec §2), so it gets its own origin.
	devZitadelPort = 8084
)
```
to:
```go
	// devZitadelPort is Zitadel's own local port-forward — Zitadel can't be reverse-proxied
	// under devGatewayPort's shared host (design spec §2), so it gets its own origin.
	devZitadelPort = 8084
	// devZitadelLoginPort is Zitadel's separate login-UI Service's own local port-forward — a
	// third, distinct origin from both devGatewayPort and devZitadelPort (see
	// docs/superpowers/specs/2026-08-11-zitadel-login-ui-routing-design.md).
	devZitadelLoginPort = 8085
)
```

Update the `InstallZitadel` call site:
```go
		zitadel, err = e2eutil.InstallZitadel(devZitadelPort,
			fmt.Sprintf("http://localhost:%d/login", devGatewayPort),
			fmt.Sprintf("http://localhost:%d/", devGatewayPort))
```
to:
```go
		zitadel, err = e2eutil.InstallZitadel(devZitadelPort, devZitadelLoginPort,
			fmt.Sprintf("http://localhost:%d/login", devGatewayPort),
			fmt.Sprintf("http://localhost:%d/", devGatewayPort))
```

Update `printStatus` — insert a third port-forward line right after the existing Zitadel one:
```go
	fmt.Println("Zitadel (needed for the login redirect above to resolve):")
	fmt.Printf("  kubectl port-forward --namespace %s svc/%s %d:8080\n\n", e2eutil.ZitadelNamespace, e2eutil.ZitadelServiceName, devZitadelPort)
```
to:
```go
	fmt.Println("Zitadel (needed for the login redirect above to resolve):")
	fmt.Printf("  kubectl port-forward --namespace %s svc/%s %d:8080\n", e2eutil.ZitadelNamespace, e2eutil.ZitadelServiceName, devZitadelPort)
	fmt.Printf("  kubectl port-forward --namespace %s svc/zitadel-login %d:3000\n\n", e2eutil.ZitadelNamespace, devZitadelLoginPort)
```
(`svc/zitadel-login` is the chart's own fixed Service name — confirmed live via `kubectl get
svc -n zitadel` during this plan's own investigation, not guessed. `e2eutil.ZitadelServiceName`
already exists as a const for the backend Service; this plan does not add an equivalent const
for `zitadel-login` — a literal string matches the existing single-use-site precedent
`ZitadelServiceName` itself was flagged as a Minor deferred item for elsewhere in the codebase,
not worth introducing new inconsistency here to fix retroactively.)

- [ ] **Step 4: Verify it builds**

```bash
go build ./... && go vet ./...
```
Expected: clean.

- [ ] **Step 5: Live verification — a real browser, end to end**

```bash
make dev-down   # clean slate — also clears any stale CRD state from prior manual testing
make dev-up
```
Expected: real output including a `kubectl port-forward ... svc/zitadel-login 8085:3000` line
alongside the existing two. Run all three printed `kubectl port-forward` commands.

Then drive a real headless Chromium against it. Playwright is not preinstalled in this
environment — set it up once:
```bash
mkdir -p /tmp/pw-zitadel-login-verify && cd /tmp/pw-zitadel-login-verify
npm init -y && npm install playwright
```
If `chromium.launch()` fails with a missing shared library (e.g. `libnspr4.so`) and there's no
root/passwordless-sudo available (`sudo -n true` fails), work around it exactly as done during
this plan's own investigation — `apt-get download` (works without root, just fetches `.deb`
files) the missing libs, `dpkg -x` each into a local directory (also no root needed), and set
`LD_LIBRARY_PATH` to that directory before running the script. The known-needed set from this
investigation: `libnspr4 libnss3 libatk1.0-0t64 libatk-bridge2.0-0t64 libcups2t64 libdrm2
libxkbcommon0 libxcomposite1 libxdamage1 libxfixes3 libxrandr2 libgbm1 libasound2t64
libgtk-3-0t64 libxshmfence1 libx11-xcb1 libxrender1 libxext6 libxi6 libcairo2 libpango-1.0-0
libpangocairo-1.0-0 libglib2.0-0t64 libatspi2.0-0t64 libdbus-1-3 libxcb1 libx11-6 libexpat1`.

Write and run a script driving the actual flow:
```javascript
import { chromium } from 'playwright';

const browser = await chromium.launch({ args: ['--no-sandbox'] });
const page = await browser.newPage();
const consoleErrors = [];
page.on('console', (msg) => { if (msg.type() === 'error') consoleErrors.push(msg.text()); });
page.on('pageerror', (err) => consoleErrors.push('pageerror: ' + err.message));

await page.goto('http://localhost:8080/', { waitUntil: 'networkidle', timeout: 30000 });
console.log('after redirect:', page.url());
await page.screenshot({ path: '01-login-form.png', fullPage: true });

// Inspect the real rendered form (username/password field selectors are not yet known —
// discover them from 01-login-form.png / page.content() first, then fill them in here).
// Zitadel's login is typically two steps: username, then password, each its own screen.
// await page.fill('<username-selector>', '<printed test username>');
// await page.click('<continue-button-selector>');
// await page.fill('<password-selector>', '<printed test password>');
// await page.click('<login-button-selector>');

await page.waitForURL(/^http:\/\/localhost:8080\//, { timeout: 15000 });
console.log('final URL after login:', page.url());
await page.screenshot({ path: '02-logged-in.png', fullPage: true });
console.log('console errors:', JSON.stringify(consoleErrors, null, 2));
await browser.close();
```
Run it in two passes: first without the fill/click lines commented in, to see the real rendered
login form and find its actual field selectors (view `01-login-form.png`, or dump
`page.content()` to a file and grep it) — then fill in the real selectors and re-run for the
full flow.

Expected, in order:
1. `01-login-form.png` shows an actual Zitadel login form (a username/email input and a
   continue/submit control) — **not** the `{"code":5,"message":"Not Found"}` JSON error this
   fix exists to eliminate.
2. After submitting the printed test-user credentials (`username: devuser`, the printed
   password), the browser ends up back at `http://localhost:8080/...` — confirm via
   `page.url()`.
3. `02-logged-in.png` shows the app's own UI (e.g. the universe picker), not a login screen or
   error page.
4. `console errors` is empty, or contains nothing alarming (a stray 404 for a favicon is fine;
   a CORS error or a failed API call is not).

If step 1 still shows the JSON error, the `BaseURI` fix didn't take effect — try the
trailing-slash variant noted in Step 1, `helm upgrade` isn't strictly needed for a `--set`
change on a fresh `dev-up` since it's a fresh install each time, so a repeat `make dev-down &&
make dev-up` after adjusting the value is the right retry loop, not a live `helm upgrade`
mid-debug.

Do not consider this task done until a real screenshot shows a rendered login form and a real
post-login authenticated app screen — this is the entire point of this fix.

```bash
make dev-down
```
Confirm clean teardown one more time.

- [ ] **Step 6: Commit**

```bash
git add test/e2e/internal/zitadel.go test/e2e/cmd/devcluster/up.go README.md
git commit -m "test/e2e/devcluster: expose Zitadel's login UI, fix browser login redirect"
```

## Final Verification

- `go build ./... && go vet ./...` clean.
- A real, live, screenshotted browser session: login form renders, login succeeds, redirect
  back to the app lands on a working authenticated session — not a curl simulation.
