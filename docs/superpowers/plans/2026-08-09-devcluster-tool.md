# `dev-up`/`dev-down` on a Kubernetes (kind) Cluster Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the docker-compose-based `make dev-up`/`make dev-down` with targets that
stand up (and tear down) the *entire* platform — all three Go binaries plus the web SPA — on a
local `kind` Kubernetes cluster, reusing the exact install/uninstall machinery `make test-e2e`
already relies on.

**Architecture:** A new Go binary, `test/e2e/cmd/devcluster`, living inside the `test/e2e/`
tree so it can import the Go-`internal`-scoped `test/e2e/internal` package (`e2eutil`)
directly. `e2eutil`'s hardcoded `"timadorus-e2e"` namespace/release name become package
variables so `devcluster` can point them at a separate `"timadorus-dev"` namespace, fully
isolated from a concurrent `make test-e2e` run. A small gitignored JSON state file lets the
separate `up`/`down` process invocations agree on what `up` actually installed (vs. found
already present), so `down` only reverses what it owns.

**Tech Stack:** Go 1.26 (unchanged), reusing `kubectl`/`helm`/`kind`/`docker` shell-outs
already used by `test/e2e/internal` — no new dependencies.

**Design spec:** `docs/superpowers/specs/2026-08-09-devcluster-tool-design.md` — read it once
for the full rationale; this plan implements it section by section (§ references below point
there).

## Global Constraints

- No new third-party Go dependencies — everything reuses `test/e2e/internal`'s existing
  shell-out pattern (`Run(exec.Command(...))`), matching this repo's established
  no-Kubernetes-client-library convention (`test/e2e/internal/run.go`'s package doc comment).
- `e2eutil.Namespace`/`e2eutil.PlatformRelease` default to `"timadorus-e2e"` unchanged — every
  existing `make test-e2e` behavior must be provably unaffected by the const→var change (Task
  1's verification is a real, live `make test-e2e` run, not just `go build`).
- `devcluster` targets a dedicated `"timadorus-dev"` namespace/release — never
  `"timadorus-e2e"` — so a dev session and a concurrent e2e run never collide (design spec §2).
- State accumulates via OR across repeated `up` invocations — a component installed by an
  earlier `up` run stays marked as dev-owned even if a later `up` run finds it already present
  (design spec §3). Never overwrite a `true` flag with `false`.
- `down` is always safe to run, including when `up` was never run or already torn down (no
  state file) — it must not error in that case, just report nothing to do.
- `docker-compose.yml` is left in place, unreferenced by the Makefile after this change — not
  deleted (design spec §1).

## File Structure Overview

```
test/e2e/internal/consts.go           # Task 1: Namespace const -> var
test/e2e/internal/platform.go          # Task 1: platformRelease -> exported var PlatformRelease;
                                        #         platformFullname const -> func PlatformFullname()
test/e2e/internal/environment.go       # Task 1: preflightCheck -> exported PreflightCheck;
                                        #         platformFullname identifier -> PlatformFullname() calls

test/e2e/cmd/devcluster/
├── state.go                           # Task 2: DevState, load/save/delete
├── up.go                              # Task 2: runUp, printStatus
├── down.go                            # Task 2: runDown, printComponentStatus
└── main.go                            # Task 2: arg parsing, dispatch

Makefile                               # Task 3: dev-up/dev-down -> go run ./test/e2e/cmd/devcluster
.gitignore                             # Task 3: + .dev-cluster-state.json
README.md                              # Task 3: Quickstart rewritten for the new dev-up
docs/PLAN.md                           # Task 3: new §16
```

## Task List

1. `e2eutil` refactor: namespace/release become variables
2. `devcluster` tool: state tracking + up/down orchestration
3. Makefile/`.gitignore`/docs wiring + full live verification

---

### Task 1: `e2eutil` refactor — namespace/release become variables

**Files:**
- Modify: `test/e2e/internal/consts.go`
- Modify: `test/e2e/internal/platform.go`
- Modify: `test/e2e/internal/environment.go`

**Interfaces:**
- Produces: `var Namespace` (was `const`), `var PlatformRelease` (was unexported `const
  platformRelease`), `func PlatformFullname() string` (was `const platformFullname`), `func
  PreflightCheck() error` (was unexported `func preflightCheck`) — all consumed by Task 2's
  `devcluster` tool.

- [ ] **Step 1: Modify `test/e2e/internal/consts.go`**

Change:
```go
package e2eutil

// Namespace holds the CloudNativePG Cluster, the JWT HMAC secret, and the
// timadorus-platform release itself. cert-manager, the Prometheus Operator, CloudNativePG's
// own operator, and NATS each get their own dedicated namespace (matching their charts' own
// conventions) — see certmanager.go/prometheus.go/postgres.go/nats.go.
const Namespace = "timadorus-e2e"
```
to:
```go
package e2eutil

// Namespace holds the CloudNativePG Cluster, the JWT HMAC secret, and the
// timadorus-platform release itself. cert-manager, the Prometheus Operator, CloudNativePG's
// own operator, and NATS each get their own dedicated namespace (matching their charts' own
// conventions) — see certmanager.go/prometheus.go/postgres.go/nats.go.
//
// A package variable, not a constant: test/e2e/cmd/devcluster overrides this to
// "timadorus-dev" before calling any install function, so a local dev session and a
// concurrent `make test-e2e` run never collide. Defaults to "timadorus-e2e", matching every
// existing caller's expectation — the e2e suite itself never sets this, it doesn't need to.
var Namespace = "timadorus-e2e"
```

- [ ] **Step 2: Modify `test/e2e/internal/platform.go`**

Change the const block:
```go
const (
	chartPath          = "deploy/helm/timadorus-platform"
	platformRelease    = "timadorus-e2e"
	commandAPIHostname = "command-api.e2e.test"
	queryAPIHostname   = "query-api.e2e.test"
	webHostname        = "web.e2e.test"

	// platformFullname mirrors the timadorus-platform chart's own
	// "timadorus-platform.fullname" template (templates/_helpers.tpl): it collapses to just
	// the release name only when the release name already *contains* the chart name. Here
	// platformRelease is "timadorus-e2e", which does not contain the chart name
	// "timadorus-platform", so Helm falls back to "<release>-<chart>" instead — confirmed via
	// `helm template` against the real chart, the same way Task 6 caught the analogous NATS
	// Service-name bug. Every Service/Deployment/Job the chart renders is named
	// "<platformFullname>-<component>", not "<platformRelease>-<component>".
	platformFullname = platformRelease + "-timadorus-platform"
)
```
to:
```go
const (
	chartPath          = "deploy/helm/timadorus-platform"
	commandAPIHostname = "command-api.e2e.test"
	queryAPIHostname   = "query-api.e2e.test"
	webHostname        = "web.e2e.test"
)

// PlatformRelease is the Helm release name for the timadorus-platform chart. A package
// variable, not a constant, for the same reason as Namespace (consts.go) —
// test/e2e/cmd/devcluster overrides it to "timadorus-dev". Defaults to "timadorus-e2e".
var PlatformRelease = "timadorus-e2e"

// PlatformFullname mirrors the timadorus-platform chart's own "timadorus-platform.fullname"
// template (templates/_helpers.tpl): it collapses to just the release name only when the
// release name already *contains* the chart name. Neither "timadorus-e2e" nor "timadorus-dev"
// contain the chart name "timadorus-platform", so Helm falls back to "<release>-<chart>"
// instead — confirmed via `helm template` against the real chart, the same way Task 6 caught
// the analogous NATS Service-name bug. Every Service/Deployment/Job the chart renders is named
// "<PlatformFullname()>-<component>", not "<PlatformRelease>-<component>". A function, not a
// const, because it depends on the now-variable PlatformRelease.
func PlatformFullname() string {
	return PlatformRelease + "-timadorus-platform"
}
```

Then, in the same file, update every remaining reference to the old unexported
`platformRelease` identifier to `PlatformRelease` (the `InstallPlatform` and
`UninstallPlatform` function bodies). The full corrected file:

```go
package e2eutil

import (
	"fmt"
	"os/exec"
)

const (
	chartPath          = "deploy/helm/timadorus-platform"
	commandAPIHostname = "command-api.e2e.test"
	queryAPIHostname   = "query-api.e2e.test"
	webHostname        = "web.e2e.test"
)

// PlatformRelease is the Helm release name for the timadorus-platform chart. A package
// variable, not a constant, for the same reason as Namespace (consts.go) —
// test/e2e/cmd/devcluster overrides it to "timadorus-dev". Defaults to "timadorus-e2e".
var PlatformRelease = "timadorus-e2e"

// PlatformFullname mirrors the timadorus-platform chart's own "timadorus-platform.fullname"
// template (templates/_helpers.tpl): it collapses to just the release name only when the
// release name already *contains* the chart name. Neither "timadorus-e2e" nor "timadorus-dev"
// contain the chart name "timadorus-platform", so Helm falls back to "<release>-<chart>"
// instead — confirmed via `helm template` against the real chart, the same way Task 6 caught
// the analogous NATS Service-name bug. Every Service/Deployment/Job the chart renders is named
// "<PlatformFullname()>-<component>", not "<PlatformRelease>-<component>". A function, not a
// const, because it depends on the now-variable PlatformRelease.
func PlatformFullname() string {
	return PlatformRelease + "-timadorus-platform"
}

// PlatformInstallInputs bundles everything InstallPlatform needs from the other installers,
// so this file has no direct dependency on postgres.go/nats.go/jwtsecret.go/gatewayapi.go
// beyond the values they hand back.
type PlatformInstallInputs struct {
	PostgresSecretName string
	NATSExternalURL    string
	GatewayClassName   string
	JWTSecretName      string
	JWTKeyID           string
	ImageTags          ImageTags
}

// imageValuesKey maps a Dockerfile/component name to its chart values key.
func imageValuesKey(component string) string {
	switch component {
	case "command-api":
		return "commandApi"
	case "query-api":
		return "queryApi"
	case "projector":
		return "projector"
	case "migrate":
		return "migration"
	default:
		return component
	}
}

// InstallPlatform runs `helm dependency update` (required for the chart to load at all, even
// though its own bundled NATS subchart won't render here) and then a single `helm upgrade
// --install` of chartPath, wiring every value described in the design spec's "Values wiring"
// table.
func InstallPlatform(in PlatformInstallInputs) error {
	if _, err := Run(exec.Command("helm", "dependency", "update", chartPath)); err != nil {
		return fmt.Errorf("e2eutil: helm dependency update: %w", err)
	}

	args := []string{
		"upgrade", "--install", PlatformRelease, chartPath,
		"--namespace", Namespace, "--create-namespace",
		"--set", "postgres.existingSecret=" + in.PostgresSecretName,
		"--set", "postgres.secretKey=uri",
		"--set", "nats.enabled=false",
		"--set", "nats.externalURL=" + in.NATSExternalURL,
		"--set", "jwt.mode=hmac",
		"--set", "jwt.hmac.existingSecret=" + in.JWTSecretName,
		"--set", "jwt.hmac.keyID=" + in.JWTKeyID,
		"--set", "gateway.gatewayClassName=" + in.GatewayClassName,
		"--set", "commandApi.route.hostname=" + commandAPIHostname,
		"--set", "queryApi.route.hostname=" + queryAPIHostname,
		"--set", "web.route.hostname=" + webHostname,
		// This Go e2e suite only exercises the command-api/query-api HTTP endpoints via
		// port-forward — it never loads the web SPA in a browser — so these web.config.*
		// values just need to be non-empty to satisfy Helm's `required` checks and let the
		// web Deployment's pod become Ready for `--wait`.
		"--set", "web.config.commandApiBaseUrl=http://placeholder.e2e.test",
		"--set", "web.config.queryApiBaseUrl=http://placeholder.e2e.test",
		"--set", "web.config.oidc.authority=http://placeholder.e2e.test",
		"--set", "web.config.oidc.clientId=e2e-placeholder",
		"--set", "web.config.oidc.redirectUri=http://placeholder.e2e.test/login",
		"--set", "web.config.oidc.postLogoutRedirectUri=http://placeholder.e2e.test/",
		"--wait", "--timeout", "5m",
	}

	for component, tag := range in.ImageTags {
		key := imageValuesKey(component)
		args = append(args,
			"--set", fmt.Sprintf("%s.image.repository=timadorus/%s", key, component),
			"--set", fmt.Sprintf("%s.image.tag=%s", key, tag),
			"--set", fmt.Sprintf("%s.image.pullPolicy=IfNotPresent", key),
		)
	}

	if _, err := Run(exec.Command("helm", args...)); err != nil {
		return fmt.Errorf("e2eutil: helm install %s: %w", PlatformRelease, err)
	}
	return nil
}

// UninstallPlatform removes the timadorus-platform release and Namespace (which also takes
// the CNPG Cluster and JWT secret with it, since they share Namespace).
func UninstallPlatform() {
	_, _ = Run(exec.Command("helm", "uninstall", PlatformRelease, "--namespace", Namespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", Namespace, "--ignore-not-found"))
}
```

- [ ] **Step 3: Modify `test/e2e/internal/environment.go`**

Export `preflightCheck` and update its one call site:
```go
func preflightCheck() error {
```
to:
```go
// PreflightCheck confirms kubectl/helm/kind/docker are all on PATH. Exported so
// test/e2e/cmd/devcluster can reuse it instead of duplicating the same four-tool check.
func PreflightCheck() error {
```

And:
```go
	if err := preflightCheck(); err != nil {
```
to:
```go
	if err := PreflightCheck(); err != nil {
```

Update the two `platformFullname` usages (now a function call, not a const identifier):
```go
	commandForward, err := StartPortForward(platformFullname+"-command-api", 18081, 8081, "/healthz", time.Minute)
```
to:
```go
	commandForward, err := StartPortForward(PlatformFullname()+"-command-api", 18081, 8081, "/healthz", time.Minute)
```
and:
```go
	queryForward, err := StartPortForward(platformFullname+"-query-api", 18082, 8082, "/healthz", time.Minute)
```
to:
```go
	queryForward, err := StartPortForward(PlatformFullname()+"-query-api", 18082, 8082, "/healthz", time.Minute)
```

(The doc comment two lines above these, currently reading `// Service names are
"<platformFullname>-<component>"...`, can stay as prose referring to the concept by its old
name — or update `platformFullname` → `PlatformFullname()` in the comment text too for
consistency; either is fine, prefer updating it for accuracy.)

- [ ] **Step 4: Verify the package builds and existing tests still pass**

```bash
go build ./... && go vet ./...
go test -tags e2e ./test/e2e/internal/... -v
```
Expected: clean build; the package's own `_test.go` files (`cluster_test.go`,
`jwtsecret_test.go`) pass unchanged — these don't touch `Namespace`/`PlatformRelease`, so this
mainly proves nothing else in the package broke syntactically.

- [ ] **Step 5: Verify `make test-e2e` still passes for real — this is the actual proof the
  refactor is behavior-preserving**

```bash
make test-e2e
```
Expected: `SUCCESS! -- 1 Passed | 0 Failed | 0 Pending | 0 Skipped`, identical to before this
task. This is a live run against a real kind cluster (several minutes) — let it run to
completion; do not skip this or assume success. If it fails, the const→var change broke
something real; do not proceed to Task 2 until this passes.

- [ ] **Step 6: Commit**

```bash
git add test/e2e/internal/consts.go test/e2e/internal/platform.go test/e2e/internal/environment.go
git commit -m "test/e2e: make Namespace/PlatformRelease overridable, export PreflightCheck/PlatformFullname"
```

---

### Task 2: `devcluster` tool — state tracking + up/down orchestration

**Files:**
- Create: `test/e2e/cmd/devcluster/state.go`
- Create: `test/e2e/cmd/devcluster/up.go`
- Create: `test/e2e/cmd/devcluster/down.go`
- Create: `test/e2e/cmd/devcluster/main.go`

**Interfaces:**
- Consumes: every exported `e2eutil` function from Task 1, unchanged signatures:
  `PreflightCheck() error`, `Namespace`/`PlatformRelease` (package vars),
  `EnsureCluster() (bool, error)`, `KindClusterName() string`, `TeardownCluster()`,
  `IsCertManagerInstalled() bool`, `InstallCertManager() error`,
  `WaitForCertManagerWebhook() error`, `UninstallCertManager()`,
  `IsPrometheusOperatorInstalled() bool`, `InstallPrometheusOperator() error`,
  `UninstallPrometheusOperator()`, `IsCloudNativePGInstalled() bool`,
  `InstallCloudNativePG() error`, `UninstallCloudNativePG()`, `IsNATSInstalled() bool`,
  `InstallNATS() error`, `UninstallNATS()`, `NATSExternalURL` (const),
  `InstallGatewayAPI() error`, `RemoveGatewayClass()`, `GatewayClassName` (const),
  `EnsurePostgresCluster() (string, error)`, `EnsureJWTSecret() (string, error)`,
  `MintToken(string) (string, error)`, `JWTSecretName`/`JWTKeyID` (consts),
  `BuildTagLoadImages(string) (ImageTags, error)`, `InstallPlatform(PlatformInstallInputs)
  error`, `UninstallPlatform()`, `PlatformFullname() string`.
- Produces: `go run ./test/e2e/cmd/devcluster up` / `... down` — consumed by Task 3's Makefile
  targets.

- [ ] **Step 1: Create `test/e2e/cmd/devcluster/state.go`**

```go
package main

import (
	"encoding/json"
	"os"
)

// statePath is the repo-root-relative path devcluster persists what it installed to, so
// `down` (a separate process from `up`) knows what to reverse. Gitignored — this is local,
// machine-specific state, never meant to be committed.
const statePath = ".dev-cluster-state.json"

// DevState records which shared, cluster-wide components devcluster itself installed (as
// opposed to found already present) across one or more `up` runs. Fields accumulate via OR
// across repeated `up` invocations: `up` loads the existing state, then only ever sets a
// field to true (never clears one back to false), so a component installed by an earlier run
// stays marked as dev-owned even if a later run finds it already present.
type DevState struct {
	CreatedCluster              bool `json:"createdCluster"`
	InstalledCertManager        bool `json:"installedCertManager"`
	InstalledPrometheusOperator bool `json:"installedPrometheusOperator"`
	InstalledCloudNativePG      bool `json:"installedCloudNativePG"`
	InstalledNATS               bool `json:"installedNATS"`
}

// loadState reads the state file, returning a zero-value DevState (nothing installed by us
// yet) if it doesn't exist — the expected case on a machine's first `up`, and also what `down`
// sees if `up` was never run: every field false, so `down` reverses nothing beyond the
// always-unconditional platform/GatewayClass cleanup it does regardless (see down.go).
func loadState() (DevState, error) {
	data, err := os.ReadFile(statePath)
	if os.IsNotExist(err) {
		return DevState{}, nil
	}
	if err != nil {
		return DevState{}, err
	}
	var s DevState
	if err := json.Unmarshal(data, &s); err != nil {
		return DevState{}, err
	}
	return s, nil
}

// saveState writes s to the state file as indented JSON.
func saveState(s DevState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath, data, 0o644)
}

// deleteState removes the state file. Safe to call even if it doesn't exist.
func deleteState() error {
	err := os.Remove(statePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
```

- [ ] **Step 2: Create `test/e2e/cmd/devcluster/up.go`**

```go
package main

import (
	"fmt"

	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

const (
	devNamespace = "timadorus-dev"
	// devCommandAPIPort/devQueryAPIPort match internal/config's own defaults (COMMAND_API_ADDR
	// ":8081", QUERY_API_ADDR ":8082"). The printed port-forward commands map local:remote as
	// 8081:8081/8082:8082 — not the e2e suite's arbitrary local port choice of 18081/18082,
	// which only exists to dodge collisions with a real `go run ./cmd/command-api` running
	// during that suite's own test runs; a freshly-printed command for a human to run
	// interactively can just use the natural mapping.
	devCommandAPIPort = 8081
	devQueryAPIPort   = 8082
)

func runUp() error {
	if err := e2eutil.PreflightCheck(); err != nil {
		return err
	}

	e2eutil.Namespace = devNamespace
	e2eutil.PlatformRelease = devNamespace

	state, err := loadState()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	createdCluster, err := e2eutil.EnsureCluster()
	if err != nil {
		return fmt.Errorf("cluster: %w", err)
	}
	if createdCluster {
		state.CreatedCluster = true
	}

	if !e2eutil.IsCertManagerInstalled() {
		if err := e2eutil.InstallCertManager(); err != nil {
			return fmt.Errorf("cert-manager: %w", err)
		}
		state.InstalledCertManager = true
	}
	if err := e2eutil.WaitForCertManagerWebhook(); err != nil {
		return fmt.Errorf("cert-manager webhook: %w", err)
	}

	if !e2eutil.IsPrometheusOperatorInstalled() {
		if err := e2eutil.InstallPrometheusOperator(); err != nil {
			return fmt.Errorf("prometheus operator: %w", err)
		}
		state.InstalledPrometheusOperator = true
	}

	if !e2eutil.IsCloudNativePGInstalled() {
		if err := e2eutil.InstallCloudNativePG(); err != nil {
			return fmt.Errorf("cloudnative-pg: %w", err)
		}
		state.InstalledCloudNativePG = true
	}

	if !e2eutil.IsNATSInstalled() {
		if err := e2eutil.InstallNATS(); err != nil {
			return fmt.Errorf("nats: %w", err)
		}
		state.InstalledNATS = true
	}

	if err := e2eutil.InstallGatewayAPI(); err != nil {
		return fmt.Errorf("gateway API: %w", err)
	}

	postgresSecret, err := e2eutil.EnsurePostgresCluster()
	if err != nil {
		return fmt.Errorf("postgres cluster: %w", err)
	}

	jwtSecret, err := e2eutil.EnsureJWTSecret()
	if err != nil {
		return fmt.Errorf("jwt secret: %w", err)
	}
	token, err := e2eutil.MintToken(jwtSecret)
	if err != nil {
		return fmt.Errorf("mint token: %w", err)
	}

	tags, err := e2eutil.BuildTagLoadImages(e2eutil.KindClusterName())
	if err != nil {
		return fmt.Errorf("build/load images: %w", err)
	}

	if err := e2eutil.InstallPlatform(e2eutil.PlatformInstallInputs{
		PostgresSecretName: postgresSecret,
		NATSExternalURL:    e2eutil.NATSExternalURL,
		GatewayClassName:   e2eutil.GatewayClassName,
		JWTSecretName:      e2eutil.JWTSecretName,
		JWTKeyID:           e2eutil.JWTKeyID,
		ImageTags:          tags,
	}); err != nil {
		return fmt.Errorf("install platform: %w", err)
	}

	if err := saveState(state); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	printStatus(token)
	return nil
}

func printStatus(token string) {
	fullname := e2eutil.PlatformFullname()
	fmt.Printf("\nDev cluster ready. Namespace: %s\n\n", devNamespace)
	fmt.Println("Port-forward the APIs in another terminal:")
	fmt.Printf("  kubectl port-forward --namespace %s svc/%s-command-api %d:%d\n",
		devNamespace, fullname, devCommandAPIPort, devCommandAPIPort)
	fmt.Printf("  kubectl port-forward --namespace %s svc/%s-query-api %d:%d\n",
		devNamespace, fullname, devQueryAPIPort, devQueryAPIPort)
	fmt.Println()
	fmt.Println("Bearer token for local calls (1 hour expiry):")
	fmt.Printf("  %s\n\n", token)
	fmt.Println("Example:")
	fmt.Printf("  curl -H \"Authorization: Bearer %s\" http://localhost:%d/universes\n\n", token, devQueryAPIPort)
}
```

- [ ] **Step 3: Create `test/e2e/cmd/devcluster/down.go`**

```go
package main

import (
	"fmt"

	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

func runDown() error {
	e2eutil.Namespace = devNamespace
	e2eutil.PlatformRelease = devNamespace

	state, err := loadState()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// UninstallPlatform and RemoveGatewayClass are always dev-owned — never gated by state,
	// exactly like e2eutil.Teardown()'s own unconditional calls to the same two functions —
	// and both tolerate "nothing to remove" (Helm uninstall / namespace delete / gatewayclass
	// delete --ignore-not-found), so this is safe even if `up` was never run.
	e2eutil.UninstallPlatform()
	e2eutil.RemoveGatewayClass()

	if state.InstalledNATS {
		e2eutil.UninstallNATS()
	}
	if state.InstalledCloudNativePG {
		e2eutil.UninstallCloudNativePG()
	}
	if state.InstalledPrometheusOperator {
		e2eutil.UninstallPrometheusOperator()
	}
	if state.InstalledCertManager {
		e2eutil.UninstallCertManager()
	}
	if state.CreatedCluster {
		e2eutil.TeardownCluster()
	}

	if err := deleteState(); err != nil {
		return fmt.Errorf("delete state: %w", err)
	}

	fmt.Println("Dev cluster torn down:")
	fmt.Printf("  - %s namespace (Helm release, Postgres cluster, JWT secret): removed\n", devNamespace)
	fmt.Println("  - Gateway API placeholder GatewayClass: removed")
	printComponentStatus("NATS", state.InstalledNATS)
	printComponentStatus("CloudNativePG operator", state.InstalledCloudNativePG)
	printComponentStatus("Prometheus operator", state.InstalledPrometheusOperator)
	printComponentStatus("cert-manager", state.InstalledCertManager)
	printComponentStatus("kind cluster", state.CreatedCluster)
	return nil
}

func printComponentStatus(name string, removedByUs bool) {
	if removedByUs {
		fmt.Printf("  - %s: removed (dev-up had installed it)\n", name)
	} else {
		fmt.Printf("  - %s: left running (was already present before dev-up)\n", name)
	}
}
```

- [ ] **Step 4: Create `test/e2e/cmd/devcluster/main.go`**

```go
// Command devcluster stands up (or tears down) the full timadorus-platform stack — all
// four Go binaries plus the web SPA — on a local kind Kubernetes cluster, replacing the old
// docker-compose-based dev-up/dev-down. It reuses the exact same install/uninstall machinery
// `make test-e2e` already relies on (test/e2e/internal, package e2eutil), targeting a
// dedicated "timadorus-dev" namespace/Helm release so it never collides with a concurrent
// `make test-e2e` run against "timadorus-e2e".
//
// One known limitation, not solved here: NATS JetStream itself (when shared/pre-existing
// rather than freshly installed by this tool) has no per-environment namespacing at the
// stream level — event streams are global, not scoped to "timadorus-dev" vs "timadorus-e2e".
// Running `make dev-up` and `make test-e2e` at the exact same moment against a machine where
// NATS was already shared-installed by a prior run can cross-contaminate each session's
// projections. Fixing this would mean namespacing NATS subjects per environment — out of
// scope for this tool.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "down") {
		fmt.Fprintln(os.Stderr, "usage: devcluster <up|down>")
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "up":
		err = runUp()
	case "down":
		err = runDown()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "devcluster:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Verify the package builds**

```bash
go build ./... && go vet ./...
```
Expected: clean. Note `test/e2e/cmd/devcluster` has no `e2e` build tag (unlike files under
`test/e2e/` itself) — it's a real CLI meant to be `go run` directly, so it must always compile
as part of the normal `go build ./...`/`go vet ./...` sweep.

- [ ] **Step 6: Verify the tool works for real — a live `up` then `down` round trip**

```bash
go run ./test/e2e/cmd/devcluster up
```
Expected: real output ending with the port-forward commands and a bearer token (matching
`printStatus`'s format above). This is a live run against a real kind cluster (several
minutes, similar to `make test-e2e` but not torn down automatically) — let it run to
completion.

Inspect `.dev-cluster-state.json` — confirm it exists and its booleans reflect what was
actually installed (e.g., on a machine where cert-manager/Prometheus/CloudNativePG/NATS were
NOT already present, all five fields should be `true`; on a machine where some were shared
from an earlier `make test-e2e` run, only the ones genuinely freshly installed by this `up`
should be `true`).

Run one of the printed `kubectl port-forward` commands in the background, then make a real
authenticated call using the printed token:
```bash
kubectl port-forward --namespace timadorus-dev svc/timadorus-dev-timadorus-platform-query-api 8082:8082 &
sleep 2
curl -H "Authorization: Bearer <token from the up output>" http://localhost:8082/universes
kill %1
```
Expected: a `200` with a JSON array (empty `[]` is fine — nothing's been created yet), proving
the whole stack — command-api → event store → outbox → NATS → projector → read model →
query-api — is genuinely up and reachable, not just "pods exist."

Then:
```bash
go run ./test/e2e/cmd/devcluster down
```
Expected: real output listing what was removed vs. left running, matching
`printComponentStatus`'s format. Confirm afterward:
```bash
kubectl get ns timadorus-dev
```
Expected: `Error from server (NotFound)` — the namespace is genuinely gone. Confirm
`.dev-cluster-state.json` no longer exists.

Do not fabricate or assume any of this output — this is a real, live, multi-minute
Kubernetes deployment and teardown; run it for real and capture the real results.

- [ ] **Step 7: Commit**

```bash
git add test/e2e/cmd/devcluster/
git commit -m "test/e2e: add devcluster tool (up/down orchestration for a full k8s dev stack)"
```

---

### Task 3: Makefile/`.gitignore`/docs wiring + full live verification

**Files:**
- Modify: `Makefile`
- Modify: `.gitignore`
- Modify: `README.md`
- Modify: `docs/PLAN.md`

**Interfaces:** none — this task wires already-working, already-verified pieces (Tasks 1-2)
into the actual `make` targets developers will use, plus documents them.

- [ ] **Step 1: Modify `Makefile`**

Change:
```makefile
dev-up:
	docker compose up -d

dev-down:
	docker compose down -v
```
to:
```makefile
dev-up:
	go run ./test/e2e/cmd/devcluster up

dev-down:
	go run ./test/e2e/cmd/devcluster down
```

- [ ] **Step 2: Modify `.gitignore`**

Add one line:
```
.dev-cluster-state.json
```

- [ ] **Step 3: Modify `README.md`'s Quickstart section**

Change:
```markdown
## Quickstart

```sh
# 1. Start Postgres + NATS JetStream
make dev-up

# 2. Apply migrations (one schema-owner tracking table per event-store/projection — see
#    docs/PLAN.md §7)
make migrate-up

# 3. Run all three binaries (separate terminals), pointing at the local infra
DATABASE_URL="postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable" \
NATS_URL="nats://localhost:4222" \
go run ./cmd/command-api

DATABASE_URL="postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable" \
NATS_URL="nats://localhost:4222" \
go run ./cmd/projector

DATABASE_URL="postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable" \
NATS_URL="nats://localhost:4222" \
go run ./cmd/query-api

# 4. Run the web SPA (separate terminal) — defaults in web/public/config.json already point
#    at the two APIs above; replace the OIDC placeholder values with a real dev IdP's before
#    testing login
cd web
npm install
npm run dev
```

With no `JWT_JWKS_URL`/`JWT_HMAC_SECRET` configured, both APIs fall back to a well-known,
loudly-logged **insecure dev HMAC secret** so they work out of the box locally — never set
this up in a real deployment (see `internal/auth`).
```
to:
```markdown
## Quickstart

```sh
make dev-up
```

`dev-up` stands up the **entire platform** — all three Go binaries plus the web SPA — on a
local `kind` Kubernetes cluster (creating one if none exists), installing whatever's missing
(cert-manager, the Prometheus Operator, CloudNativePG, NATS JetStream, the Gateway API) and
deploying the platform itself freshly built from your current code, via the real Helm chart
(`deploy/helm/timadorus-platform`) — the same one used in production and by `make test-e2e`
(against a separate, non-colliding namespace). When it's ready, it prints something like:

```
Dev cluster ready. Namespace: timadorus-dev

Port-forward the APIs in another terminal:
  kubectl port-forward --namespace timadorus-dev svc/timadorus-dev-timadorus-platform-command-api 8081:8081
  kubectl port-forward --namespace timadorus-dev svc/timadorus-dev-timadorus-platform-query-api 8082:8082

Bearer token for local calls (1 hour expiry):
  eyJhbGciOi...
```

Run those two `port-forward` commands (each in its own terminal, or backgrounded), then:

```sh
curl -H "Authorization: Bearer <token>" http://localhost:8082/universes
```

When you're done:

```sh
make dev-down
```

`dev-down` only removes what `dev-up` itself installed — if cert-manager/the Prometheus
Operator/CloudNativePG/NATS were already on your cluster for some other reason, they're left
running.

### Faster local iteration without Kubernetes

Rebuilding a Docker image and running a Helm upgrade on every code change is slower than a
plain `go run`. For tight iteration loops, `docker-compose.yml` (Postgres + NATS JetStream
only, no Kubernetes) is still available directly — not through a `make` target, since
`dev-up`/`dev-down` now mean the Kubernetes flow above:

```sh
docker compose up -d
make migrate-up

DATABASE_URL="postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable" \
NATS_URL="nats://localhost:4222" \
go run ./cmd/command-api

# ...same pattern for ./cmd/projector and ./cmd/query-api in their own terminals

cd web && npm install && npm run dev   # web SPA, separate terminal
```

With no `JWT_JWKS_URL`/`JWT_HMAC_SECRET` configured, both APIs fall back to a well-known,
loudly-logged **insecure dev HMAC secret** so they work out of the box locally — never set
this up in a real deployment (see `internal/auth`). Stop with `docker compose down -v`.
```

- [ ] **Step 4: Add a new `docs/PLAN.md` §16**, after the end of §15's content, before the
  document's final `## Verification` section:

```markdown
## 16. Local Development on Kubernetes (`make dev-up`/`make dev-down`)

`make dev-up`/`make dev-down` deploy the entire platform — all three Go binaries plus the web
SPA — onto a local `kind` cluster via the real Helm chart
(`deploy/helm/timadorus-platform`), replacing an earlier docker-compose-only version of these
targets. The implementation (`test/e2e/cmd/devcluster`) reuses `test/e2e/internal`'s
install/uninstall machinery verbatim — the same code `make test-e2e` runs against — targeting
a dedicated `timadorus-dev` namespace/Helm release so a dev session and a concurrent
`make test-e2e` run never collide. A small gitignored state file
(`.dev-cluster-state.json`) tracks which shared, cluster-wide components (cert-manager, the
Prometheus Operator, CloudNativePG, NATS) `dev-up` itself installed, so `dev-down` only
reverses what it owns rather than tearing down infrastructure something else on the cluster
still depends on.

Full design: `docs/superpowers/specs/2026-08-09-devcluster-tool-design.md`.

**Known limitation:** NATS JetStream event streams are global, not namespaced per environment
— running `dev-up` and `test-e2e` at the exact same moment against a machine where NATS was
already shared-installed can cross-contaminate each session's projections. Not solved here;
avoid running both simultaneously on a machine with pre-existing shared NATS.
```

- [ ] **Step 5: Full live verification**

Run each of these in sequence, capturing real output — this proves the Makefile wiring itself
works (not just the underlying tool, already proven in Task 2) and validates the two scenarios
the design spec's Verification section calls out:

```bash
# 1. The actual make targets work end to end
make dev-up
```
Expected: real output identical in shape to Task 2's verification. Then:
```bash
make dev-down
```
Expected: clean teardown, matching Task 2's verification.

```bash
# 2. Namespace isolation holds under a real concurrent run
make dev-up
```
Wait for it to finish, then, without tearing down:
```bash
make test-e2e
```
Expected: `make test-e2e` passes on its own (`SUCCESS! -- 1 Passed | 0 Failed`), and while it's
running, `kubectl get ns` briefly shows both `timadorus-dev` and `timadorus-e2e` — confirm this
by running `kubectl get ns` in another terminal shortly after starting `make test-e2e`. After
`make test-e2e` finishes (it tears down `timadorus-e2e` itself), confirm `timadorus-dev` is
still present and unaffected:
```bash
kubectl get ns timadorus-dev
kubectl get pods -n timadorus-dev
```
Expected: namespace present, pods still `Running`/`Ready` — proving the two never collided.
Then:
```bash
make dev-down
```

```bash
# 3. State accumulation across repeated `dev-up` runs
make dev-up
cat .dev-cluster-state.json   # note which fields are true
make dev-up                   # run it again — everything should already be present
cat .dev-cluster-state.json   # the same fields must still be true, not reset to false
make dev-down
```
Expected: the second `.dev-cluster-state.json` is identical to the first (the accumulation
rule from Global Constraints held) — the state file's `true` flags survive a second `up` run
that found everything already present rather than installing anything itself.

Do not fabricate or assume any of this output. Every one of these three scenarios involves a
real, live, multi-minute Kubernetes deployment — run each for real.

- [ ] **Step 6: Commit**

```bash
git add Makefile .gitignore README.md docs/PLAN.md
git commit -m "docs,build: wire devcluster into make dev-up/dev-down, document the new flow"
```

---

## Final Verification (whole feature)

After all 3 tasks:

- `go build ./... && go vet ./...` clean, including the new `test/e2e/cmd/devcluster` package.
- `make test-e2e` passes live (proves Task 1's refactor didn't regress the existing suite).
- `make dev-up` then `make dev-down` pass live, individually and back-to-back (Task 2/3).
- `make dev-up` + `make test-e2e` run concurrently without collision (Task 3, namespace
  isolation holds for real, not just in the design).
- Two consecutive `make dev-up` runs produce an unchanged, correctly-accumulated
  `.dev-cluster-state.json` (Task 3, state accumulation rule holds for real).
