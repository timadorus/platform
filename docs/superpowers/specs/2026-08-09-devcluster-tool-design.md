# `dev-up`/`dev-down` on a Kubernetes (kind) Cluster — Design Spec

## Context

`make dev-up`/`make dev-down` currently start/stop a `docker-compose.yml` stack (Postgres +
NATS JetStream only) for local development against `go run ./cmd/<binary>`. Separately,
`test/e2e/internal` (package `e2eutil`) already contains a complete, working
install/configure/teardown pipeline for running the *entire* platform — all three Go binaries
plus the new web SPA — on a `kind` Kubernetes cluster via the real Helm chart
(`deploy/helm/timadorus-platform`), used today only by `make test-e2e`.

This spec replaces the docker-compose-based `dev-up`/`dev-down` with new targets that stand up
(and tear down) the full stack on the same kind cluster, using the exact same, already-proven
install machinery — not a second, drifting implementation.

## 1. Tool Location & Reuse

A new binary, `test/e2e/cmd/devcluster/main.go`, living inside the `test/e2e/` tree so it can
import the Go-`internal`-scoped `test/e2e/internal` package (package `e2eutil`) directly — no
promotion of that package out of `internal`, no duplicated install logic. The Makefile targets
become thin wrappers:

```makefile
dev-up:
	go run ./test/e2e/cmd/devcluster up

dev-down:
	go run ./test/e2e/cmd/devcluster down
```

The old `docker-compose.yml`-based targets are removed. `docker-compose.yml` itself is left in
place (not deleted by this change) in case it's still useful for something narrower later, but
`make dev-up`/`make dev-down` no longer reference it.

## 2. Namespace/Release Isolation from `make test-e2e`

`e2eutil` currently hardcodes a single Kubernetes namespace and Helm release name
(`const Namespace = "timadorus-e2e"` in `consts.go`; an unexported `const platformRelease =
"timadorus-e2e"` in `platform.go`) shared by the Postgres `Cluster` CR, the JWT HMAC secret,
and the `timadorus-platform` Helm release itself. If `dev-up` reused these unchanged, running
`make test-e2e` while a dev session is up would silently `helm upgrade --install` over it mid-session and then delete it when the test's `AfterSuite` tears down.

**Fix:** both become **package variables**, not constants, in `e2eutil`:

```go
// consts.go
var Namespace = "timadorus-e2e"
```

```go
// platform.go
var PlatformRelease = "timadorus-e2e"

// PlatformFullname mirrors the timadorus-platform chart's own "timadorus-platform.fullname"
// template — see the original doc comment on the (former) platformFullname const for the
// collapse-when-substring rule. Now a function, not a const, because it depends on the
// now-variable PlatformRelease.
func PlatformFullname() string {
	return PlatformRelease + "-timadorus-platform"
}
```

Every existing read of `Namespace` (postgres.go, jwtsecret.go, platform.go, portforward.go)
needs no change — Go doesn't distinguish `const` from `var` at call sites for simple reads.
The one caller of the former `platformFullname` const (`environment.go`, building the
port-forwarded Service names) becomes `PlatformFullname()`. `e2e_suite_test.go`'s `BeforeSuite`
does not need to set `Namespace`/`PlatformRelease` explicitly — they default to
`"timadorus-e2e"`, identical to today's behavior, so `make test-e2e` is unaffected.

`devcluster up`/`devcluster down` set both variables to `"timadorus-dev"` before calling any
`e2eutil` function.

**Deliberately not addressed:** NATS JetStream itself (`nats.go`) lives in its own
fixed, non-`Namespace`-scoped namespace (`"nats"`) with global, non-namespaced stream names
(`events_universe`, `events_user`, …). If NATS is already installed (shared, not freshly
installed by either `dev-up` or a given `test-e2e` run) and a dev session and an e2e run
happen to execute *concurrently*, their events cross-contaminate each other's projections even
though Postgres and the Helm release are now fully isolated. This is a pre-existing property of
the event bus design, not introduced by this change, and fixing it would mean namespacing NATS
subjects per environment — out of scope here. Documented as a comment at the point this matters
(`devcluster`'s top-level doc comment), not solved.

## 3. State Tracking Across the `up`/`down` Process Boundary

Unlike the e2e suite (one Go test process, in-memory `Environment` struct tracking what
`Setup()` itself installed vs. found already present, read back by `Teardown()` in the same
process), `dev-up` and `dev-down` are two separate `go run` invocations. A small JSON state
file persists this across that boundary.

**File:** `.dev-cluster-state.json` at the repository root, gitignored.

**Shape:**
```go
// test/e2e/cmd/devcluster/state.go
type DevState struct {
	CreatedCluster              bool `json:"createdCluster"`
	InstalledCertManager        bool `json:"installedCertManager"`
	InstalledPrometheusOperator bool `json:"installedPrometheusOperator"`
	InstalledCloudNativePG      bool `json:"installedCloudNativePG"`
	InstalledNATS                bool `json:"installedNATS"`
}
```

**Accumulation rule (the one subtlety):** a component already marked `true` in a prior
`dev-up` run's state must stay `true` even if a *later* `dev-up` run finds it already present
(because it's still dev-managed — a previous `dev-up` is what put it there). So `up`:

1. Loads the existing state file if present (all-`false` zero value if not).
2. Runs the install sequence, each step producing a "did I install this *this run*" bool.
3. Writes back `newState.X = oldState.X || installedThisRunX` for every field, then persists.

`down`:

1. Loads the state file. If it doesn't exist, print `"nothing to tear down (no
   .dev-cluster-state.json found)"` and exit 0 — safe, idempotent, callable even if `dev-up`
   was never run or a previous `dev-down` already ran.
2. Tears down the always-dev-owned pieces unconditionally (never gated by the state file,
   exactly like `e2eutil.Teardown()`'s own unconditional calls): `UninstallPlatform()` (removes
   the `timadorus-dev` namespace, which takes the Postgres `Cluster` CR and JWT secret with it)
   and `RemoveGatewayClass()`.
3. For each of `InstalledNATS` / `InstalledCloudNativePG` / `InstalledPrometheusOperator` /
   `InstalledCertManager`: if `true`, call the matching `e2eutil.UninstallX()`.
4. If `CreatedCluster`: `e2eutil.TeardownCluster()` (deletes the whole kind cluster).
5. Delete the state file.
6. Print a one-line confirmation of what was removed vs. left running (shared infra another
   tool/session still owns).

## 4. `devcluster up` — Full Sequence

Reusing `e2eutil` functions exactly as `environment.go`'s `Setup()` already sequences them,
minus the final port-forward-holding step (this tool prints the command instead of holding the
forward open itself — see §5) and with the state-accumulation wrapper from §3:

1. `e2eutil.PreflightCheck()` (currently unexported `preflightCheck` in `environment.go` —
   export it so `devcluster` can reuse it instead of duplicating the four-tool PATH check).
2. Set `e2eutil.Namespace = "timadorus-dev"`, `e2eutil.PlatformRelease = "timadorus-dev"`.
3. Load prior `DevState`.
4. `EnsureCluster()` → accumulate `CreatedCluster`.
5. `IsCertManagerInstalled()` / `InstallCertManager()` / `WaitForCertManagerWebhook()` →
   accumulate `InstalledCertManager`.
6. `IsPrometheusOperatorInstalled()` / `InstallPrometheusOperator()` → accumulate
   `InstalledPrometheusOperator`.
7. `IsCloudNativePGInstalled()` / `InstallCloudNativePG()` → accumulate `InstalledCloudNativePG`.
8. `IsNATSInstalled()` / `InstallNATS()` → accumulate `InstalledNATS`.
9. `InstallGatewayAPI()` — unconditional, matching `Setup()` (idempotent apply, not tracked in
   state — the CRDs are never uninstalled by either `test-e2e` or `dev-down`, only the
   placeholder `GatewayClass` is, and that's handled unconditionally in `down`, see §3).
10. `EnsurePostgresCluster()` → `postgresSecret`.
11. `EnsureJWTSecret()` → `secret`; `MintToken(secret)` → `token` (needed for the printed status
    in §5, and because `InstallPlatform` needs the secret name regardless).
12. `BuildTagLoadImages(KindClusterName())` → `tags`. This now builds **five** images, not
    four — `Dockerfile.web` alongside `Dockerfile.command-api`/`Dockerfile.query-api`/
    `Dockerfile.projector`/`Dockerfile.migrate` — since `imageComponents` in `images.go`
    already includes `"web"` (added when the e2e harness was updated to deploy the web SPA).
    No change needed here; `devcluster` gets this for free.
13. `InstallPlatform(PlatformInstallInputs{...})` — same struct, same fields, now deploying
    into the `timadorus-dev` namespace/release because of the variables set in step 2. This
    also deploys the `web` component (Deployment/Service/HTTPRoute/ConfigMap), same as
    `test-e2e` does today.
14. Persist `DevState` (OR'd with the loaded one, per §3) to `.dev-cluster-state.json`.
15. Print the status block (§5).

Every step above reuses an existing, already-tested `e2eutil` function verbatim except the one
export (`PreflightCheck`) and the two visibility changes from §2 — this is a thin orchestration
layer, not a reimplementation.

## 5. Status Output

After a successful `up`, print (to stdout):

```
Dev cluster ready. Namespace: timadorus-dev

Port-forward the APIs in another terminal:
  kubectl port-forward --namespace timadorus-dev svc/timadorus-dev-timadorus-platform-command-api 8081:8081
  kubectl port-forward --namespace timadorus-dev svc/timadorus-dev-timadorus-platform-query-api 8082:8082

Bearer token for local calls (expires per the JWT test-signing default):
  eyJhbGciOi...

Example:
  curl -H "Authorization: Bearer eyJhbGciOi..." http://localhost:8082/universes
```

The two `kubectl port-forward` lines are built from `e2eutil.PlatformFullname()` +
`-command-api`/`-query-api` (matching the real Service names the chart creates) and the
container ports already known from `internal/config`'s defaults (8081/8082) — not the
`18081`/`18082` local ports the e2e suite happens to use for its own forwards, since those are
arbitrary local-only choices to dodge collisions with a real `go run ./cmd/command-api`; a
freshly-printed command for a human to run interactively can just use the natural
`8081:8081`/`8082:8082` mapping.

The bearer token comes from the same `MintToken(secret)` call already needed internally
(step 11) — surfaced to the developer as a convenience beyond what was strictly asked for,
since the platform requires one for every request and minting it is already free at this point
in the sequence.

## 6. `devcluster down` — Full Sequence

See §3's numbered steps 1-6. No new `e2eutil` functions are needed here — `UninstallPlatform`,
`RemoveGatewayClass`, `UninstallNATS`, `UninstallCloudNativePG`, `UninstallPrometheusOperator`,
`UninstallCertManager`, and `TeardownCluster` all already exist and are reused verbatim, gated
by the loaded `DevState` exactly as described in §3.

## 7. Files Touched

```
test/e2e/internal/consts.go          # Namespace: const -> var
test/e2e/internal/platform.go         # platformRelease -> exported var PlatformRelease;
                                       # platformFullname const -> func PlatformFullname()
test/e2e/internal/environment.go      # preflightCheck -> exported PreflightCheck;
                                       # platformFullname identifier -> PlatformFullname() calls
test/e2e/cmd/devcluster/main.go       # new: up/down subcommands, orchestration
test/e2e/cmd/devcluster/state.go      # new: DevState struct, load/save
Makefile                              # dev-up/dev-down: docker compose -> go run ./test/e2e/cmd/devcluster
.gitignore                            # + .dev-cluster-state.json
docs/PLAN.md                          # brief mention alongside the existing e2e-tooling section, if one exists
README.md                             # Quickstart: docker-compose-based dev-up/dev-down instructions updated
```

`docker-compose.yml` is left in place, unreferenced by the Makefile after this change.

## 8. Explicitly Out of Scope

- Namespacing NATS JetStream subjects/streams per environment (§2's caveat) — a bigger change
  to the event-bus design, not attempted here.
- A `dev-status`/`dev-logs` convenience target — not asked for; `kubectl -n timadorus-dev get
  pods` and `kubectl -n timadorus-dev logs ...` remain the direct tools for that.
- Concurrent multiple named dev environments (e.g. `dev-up NAME=feature-x`) — this spec is one
  fixed `timadorus-dev` namespace/release, matching the single docker-compose stack it replaces.
- Automatically re-running `dev-up` on file changes (a `watch` mode) — out of scope, this
  replaces `docker-compose up -d`'s one-shot semantics, not a hot-reload dev loop.

## Verification

- `go build ./...` and `go vet ./...` clean, including the new `test/e2e/cmd/devcluster`
  package (note: this package has no `e2e` build tag, unlike the test files under `test/e2e/`
  itself, since it's a real CLI tool meant to be `go run` directly, not a test).
- `go test -tags e2e -count=1 ./test/e2e/... -timeout 30m` (i.e. `make test-e2e`) still passes
  unmodified after the `Namespace`/`PlatformRelease` const→var changes — proving
  `"timadorus-e2e"` defaults are preserved and nothing in the existing suite broke.
- Manual: `make dev-up` on a machine with no pre-existing kind cluster and no shared infra
  installed — confirm every component gets installed, the platform becomes reachable via the
  printed port-forward commands + token (a real `curl`/`timadorusctl` call against the
  port-forwarded query-api succeeds), and `.dev-cluster-state.json` reflects all five components
  as dev-installed.
- Manual: `make dev-down` immediately after — confirm the `timadorus-dev` namespace, the
  shared cert-manager/Prometheus-operator/CloudNativePG-operator/NATS installs, and the kind
  cluster itself are all removed, and `.dev-cluster-state.json` is deleted.
- Manual: run `make dev-up` once, then `make test-e2e`, then check `kubectl get ns` shows both
  `timadorus-dev` (still running, untouched) and (transiently, during the test) `timadorus-e2e`
  — confirming the namespace isolation from §2 actually holds under a real concurrent run, not
  just in theory.
- Manual: run `make dev-up` twice in a row (simulating "already up, ran it again") — confirm
  the second run detects everything already present, still updates the Helm release (uses the
  current build), and `.dev-cluster-state.json`'s `true` flags from the first run survive
  unchanged (the accumulation rule from §3).
