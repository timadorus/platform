# Kubernetes End-to-End Test Tool

## Context

`docs/PLAN.md` §11 (Testing Strategy) explicitly flags an automated vertical-slice
end-to-end test as a known, deliberate gap: the platform's create→query round trip across all
three binaries (command-api, query-api, projector) has only ever been exercised manually via
curl/httpie against a local docker-compose stack, and against a real Kubernetes cluster once,
by hand, during the Helm chart's Task 10 verification
(`docs/superpowers/plans/2026-07-25-helm-chart.md`). This spec closes that gap by building a
real, automated Kubernetes e2e test tool, modeled on the Kubebuilder/Operator-SDK scaffolded
e2e pattern (`test/e2e/e2e_suite_test.go` + `test/utils/utils.go`): detect or provision a kind
cluster, install cert-manager + a trimmed Prometheus Operator stack + CloudNativePG + NATS via
Helm, build and load the platform's own images, install the platform's own Helm chart from the
working directory, and run a real test that creates one of each of the six aggregate types via
command-api and reads them back via query-api.

`test/e2e/` already exists in the repo (empty), reserved for exactly this.

## Goals

- Detect a reachable Kubernetes cluster via the current kubeconfig context; if none is
  reachable, detect an existing named `kind` cluster; if neither exists, create one with `kind
  create cluster`.
- Install, via Helm, and verify readiness of: cert-manager (with an explicit wait for the
  `cert-manager-webhook` Deployment to become `Available`), a trimmed
  `kube-prometheus-stack` (Prometheus Operator + Prometheus only — Grafana/Alertmanager/
  node-exporter/kube-state-metrics disabled), CloudNativePG (operator + a single-instance
  `Cluster`), and a standalone NATS JetStream release.
- Build the `command-api`, `query-api`, `projector`, and `migrate` Docker images from the
  working directory's existing Dockerfiles, tag each with its own image content digest
  (SHA256, hex only), `kind load` them into the cluster, and pass those exact tags into the
  Helm install of `deploy/helm/timadorus-platform` (also from the working directory).
- Wire the platform chart's existing `existingSecret`/`externalURL`/`hmac` toggles to the
  installed dependencies with no manual Secret pre-creation beyond what this tool itself
  automates: CloudNativePG's own auto-generated connection secret for Postgres, the standalone
  NATS release's Service DNS name, and a JWT HMAC secret this tool creates and also uses
  locally to mint bearer tokens.
- Run a Ginkgo/Gomega spec that creates one User, one Universe, one Campaign, one Entity, one
  Object, and one Character (whose paired Entity is auto-created) via command-api, then polls
  query-api until each reads back correctly — covering all six aggregate types in
  `docs/PLAN.md` §2 in one pass.
- Track which components this run itself installed (mirroring the blueprint's
  `shouldCleanup*` flags) so cleanup never tears down a pre-existing cluster or pre-existing
  installs it didn't create.

## Non-goals

- No real Gateway API controller/implementation. Gateway API CRDs (pinned `v1.6.1`, same
  version already verified in the Helm chart's own Task 10) are installed only because the
  chart's `Gateway`/`HTTPRoute` objects cannot be created at all without the CRDs present — a
  hard technical prerequisite, not a routing path this tool exercises. A placeholder
  `GatewayClass` satisfies `gateway.gatewayClassName`. Test traffic reaches command-api/
  query-api via `kubectl port-forward`, never through the Gateway.
- No CI integration in this iteration. `.github/workflows/ci.yml` gets no new job; a future
  `e2e.yml` provisioning its own kind cluster (e.g. via `helm/kind-action`) is a reasonable
  follow-up, not built here.
- No coverage of every command in the platform (rename, archive, add/remove gamemaster,
  reassign player, etc.) — only the single create-then-read-back happy path per aggregate
  type, matching the literal request. Deeper e2e coverage is a future iteration.
- No teardown of a cluster this tool did not create itself. If a cluster was already reachable
  (this environment's long-lived `kind-kind` context, in particular) or an install was already
  present, cleanup leaves it exactly as found.
- Bitnami PostgreSQL is not used (CloudNativePG was chosen instead, per explicit decision).

## Architecture

```
test/e2e/
  e2e_suite_test.go       # Ginkgo TestE2E entrypoint; BeforeSuite/AfterSuite orchestration
  e2e_test.go             # Describe/It: create one of each aggregate, read back via query-api
  internal/
    run.go                 # shared exec.Command runner + combined-output capture (mirrors blueprint's Run())
    cluster.go             # EnsureCluster(): reachable-context check -> existing named kind -> kind create
    certmanager.go         # IsInstalled/Install/Uninstall (Helm: jetstack/cert-manager) + webhook wait
    prometheus.go          # IsInstalled/Install/Uninstall (Helm: kube-prometheus-stack, trimmed)
    postgres.go            # IsInstalled/Install/Uninstall (Helm: cnpg/cloudnative-pg) + Cluster CR + wait
    nats.go                 # IsInstalled/Install/Uninstall (Helm: nats/nats, standalone release)
    gatewayapi.go            # Install Gateway API CRDs v1.6.1 + placeholder GatewayClass
    jwtsecret.go             # generate HMAC secret, create k8s Secret, mint HS256 bearer tokens (jwx/v3)
    images.go                # build + digest-tag + kind-load command-api/query-api/projector/migrate
    platform.go              # helm dependency update + helm install/upgrade of ./deploy/helm/timadorus-platform
    portforward.go           # start/stop `kubectl port-forward` for command-api & query-api Services
```

Namespace convention: cert-manager, kube-prometheus-stack, CloudNativePG's operator, and NATS
each install into their own dedicated namespace (`cert-manager`, `monitoring`, `cnpg-system`,
`nats` respectively, matching their charts' own conventions). The CNPG `Cluster` CR, the JWT
secret, and the `timadorus-platform` release itself all live together in one test namespace,
`timadorus-e2e` (created by this tool if absent).

All installer files (`certmanager.go`, `prometheus.go`, `postgres.go`, `nats.go`) follow the
same three-function shape as the blueprint (`IsXInstalled() bool`, `InstallX() error`,
`UninstallX()`), each backed by a Helm release/CRD-presence check, so `BeforeSuite` and
`AfterSuite` can symmetrically skip install/uninstall for anything already present.

### `BeforeSuite` sequence

1. `cluster.EnsureCluster()` — sets `didCreateCluster` if it had to run `kind create cluster`.
2. `certmanager.EnsureInstalled()` — Helm install `jetstack/cert-manager` (namespace
   `cert-manager`, `--set crds.enabled=true`) if not already present (CRD-presence check:
   `certificates.cert-manager.io` etc.), then **always** `kubectl wait
   deployment/cert-manager-webhook -n cert-manager --for=condition=Available --timeout=5m`
   regardless of whether this run installed it or found it already there — satisfies "verify
   the webhook has become available" literally.
3. `prometheus.EnsureInstalled()` — Helm install `prometheus-community/kube-prometheus-stack`
   (namespace `monitoring`) with `--set grafana.enabled=false --set alertmanager.enabled=false
   --set nodeExporter.enabled=false --set kubeStateMetrics.enabled=false`, if not already
   present (CRD-presence check: `prometheuses.monitoring.coreos.com`).
4. `postgres.EnsureInstalled()` — Helm install `cnpg/cloudnative-pg` (namespace `cnpg-system`)
   if not already present (CRD-presence check: `clusters.postgresql.cnpg.io`), then `kubectl
   apply` a one-instance, 1Gi-storage `Cluster` named `timadorus-pg` in the test namespace
   (`timadorus-e2e`, created if absent) and wait for its `Ready` condition
   (`kubectl wait cluster.postgresql.cnpg.io/timadorus-pg --for=condition=Ready --timeout=5m`).
5. `nats.EnsureInstalled()` — Helm install `nats/nats` (namespace `nats`, release name `nats`,
   `--set config.jetstream.enabled=true`) if not already present.
6. `gatewayapi.EnsureInstalled()` — `kubectl apply` the Gateway API v1.6.1 standard-install
   manifest (idempotent — CRDs already existing is a no-op) and a placeholder `GatewayClass`.
7. `jwtsecret.EnsureInstalled()` — generate a random secret, `kubectl create secret generic
   jwt-hmac-secret --from-literal=JWT_HMAC_SECRET=<value>` in the test namespace
   (`timadorus-e2e`); keep the value in memory for minting tokens later.
8. `images.BuildTagLoad()` — for each of the four images: `docker build`, compute the content
   digest via `docker inspect --format='{{.Id}}'`, strip the `sha256:` prefix, `docker tag` to
   that hex string, `kind load docker-image` into the target cluster; returns a
   `map[string]string` of component → digest tag.
9. `platform.Install()` — `helm dependency update ./deploy/helm/timadorus-platform` (needed so
   the chart loads at all, even though the vendored NATS subchart won't render with
   `nats.enabled=false`), then a single `helm upgrade --install` with every `--set` override
   described in the Values Wiring section below, `--wait --timeout 5m`.
10. `portforward.Start()` — `kubectl port-forward` to the command-api and query-api Services
    on two local ports, polling `/healthz` on each until it responds before returning.

### `AfterSuite` sequence (reverse order, each step conditional on "did this run install it")

`portforward.Stop()` → `platform.Uninstall()` (release + test namespace) →
`nats.Uninstall()` (if installed here) → `postgres.Uninstall()` (if installed here) →
`prometheus.Uninstall()` (if installed here) → `certmanager.Uninstall()` (if installed here) →
`gatewayapi.RemoveGatewayClass()` (CRDs are left installed, matching the Helm chart's own
Task 10 precedent) → `cluster.TeardownIfCreated()` (deletes the kind cluster only if
`didCreateCluster`; skippable via `E2E_KEEP_CLUSTER=true` for local debugging, mirroring the
blueprint's `CERT_MANAGER_INSTALL_SKIP`-style env-var escape hatch).

## Values wiring for the `timadorus-platform` chart install

| Chart value | Source |
|---|---|
| `postgres.existingSecret` | `timadorus-pg-app` (CloudNativePG's own auto-generated secret, naming convention `<cluster-name>-app`) |
| `postgres.secretKey` | `uri` (CNPG's app secret contains a ready-to-use `postgresql://user:pass@host:port/dbname` connection string under this key — confirmed against CNPG's docs, not assumed) |
| `nats.enabled` | `false` |
| `nats.externalURL` | `nats://nats-nats.nats.svc.cluster.local:4222` (standalone release name `nats` + chart name `nats` ⇒ Service `nats-nats`, same naming convention already verified for the chart's own bundled NATS dependency) |
| `jwt.mode` | `hmac` |
| `jwt.hmac.existingSecret` | `jwt-hmac-secret` (created by this tool) |
| `jwt.hmac.keyID` | `"e2e"` (fixed literal), must match the `kid` header the test's token-minting code sets |
| `gateway.gatewayClassName` | placeholder `GatewayClass` this tool creates |
| `commandApi.route.hostname` / `queryApi.route.hostname` | placeholder hostnames (`command-api.e2e.test` / `query-api.e2e.test`) — required by the chart's `HTTPRoute` templates even though traffic never uses them |
| `commandApi.image.{repository,tag,pullPolicy}` / `queryApi.*` / `projector.*` / `migration.*` | repository unchanged (`timadorus/<name>`), tag = that image's SHA256 digest hex, `pullPolicy: IfNotPresent` (the image was `kind load`ed directly onto the node, never pulled from a registry) |

## Test spec (`e2e_test.go`)

One `It` block, using Gomega's `Eventually()` for every query-api read (the projector is
eventually consistent):

1. `POST /users {name}` → `userID`.
2. `POST /universes {name, creatorUserIds:[userID]}` → `universeID`.
3. `POST /universes/{universeID}/campaigns {name, gamemasterUserIds:[userID]}` → `campaignID`.
4. `POST /universes/{universeID}/entities {name}` → `entityID` (standalone Entity).
5. `POST /universes/{universeID}/objects {name}` → `objectID`.
6. `POST /campaigns/{campaignID}/characters {name, playerUserId:userID}` →
   `{characterID, entityID: characterEntityID}` (the atomic Character+Entity creation).
7. `Eventually(GET /users/{userID})` → name matches, `isArchived == false`.
8. `Eventually(GET /universes/{universeID})` → name matches, `isArchived == false`.
9. `Eventually(GET /campaigns/{campaignID})` → name and `universeId` match, `isArchived ==
   false`.
10. `Eventually(GET /entities/{entityID})` → name and `universeId` match.
11. `Eventually(GET /objects/{objectID})` → name and `universeId` match.
12. `Eventually(GET /characters/{characterID})` → name, `campaignId`, `entityId
    == characterEntityID`, `playerUserId == userID`, `isArchived == false`.

Every request (command and query alike) carries `Authorization: Bearer <token>`, one token
minted once in `BeforeSuite` (`sub` = a fixed test UUID, `kid` matching
`jwt.hmac.keyID`, alg `HS256`, signed with the same secret installed in
`jwt-hmac-secret`), via `github.com/lestrrat-go/jwx/v3/jwt` — already a direct dependency, no
new import needed for signing.

## New dependencies

- `github.com/onsi/ginkgo/v2` and `github.com/onsi/gomega` — new, isolated to `test/e2e/`.
- No Kubernetes Go client library (client-go/controller-runtime) — every cluster interaction
  shells out to `kubectl`/`helm`/`kind`/`docker`, matching both the blueprint's own style and
  this repo's existing no-framework, shell-out convention (`scripts/migrate-up.sh`).

## New Makefile target

```makefile
test-e2e:
	go test ./test/e2e/... -v -timeout 30m
```

## Testing / verification

- `go build ./test/e2e/...` compiles cleanly once Ginkgo/Gomega are added to `go.mod`.
- `make test-e2e` run once, in full, against this environment's existing `kind-kind` cluster:
  confirms cluster-reachability detection takes the "already reachable" branch (no `kind
  create cluster` invoked), every installer's `IsXInstalled()` correctly reports `false` on a
  clean cluster and `true` on a second run (idempotency), the four images build and load with
  distinct digest tags, the platform chart installs successfully with all values wired as
  above, the migration Job completes, and the Ginkgo spec passes end to end (all six aggregate
  types created and read back correctly).
- A second `make test-e2e` run immediately after the first (before any manual cleanup)
  exercises the "already installed" detection path for every component and should also pass,
  proving cleanup correctly left cert-manager/Prometheus/CloudNativePG/NATS/Gateway API CRDs
  in place from the first run.
