# Helm Chart: `timadorus-platform`

## Context

The platform is three Go binaries (`command-api`, `query-api`, `projector`; see `docs/PLAN.md`
§3/§12) already built as Docker images via `Dockerfile.command-api`, `Dockerfile.query-api`,
`Dockerfile.projector`, each configured entirely through environment variables read in
`internal/config/config.go`, and each exposing `/healthz`, `/readyz`, `/metrics` on its own
`HTTPAddr` port (8081/8082/8083 respectively). This spec adds a Helm chart at
`deploy/helm/timadorus-platform` to deploy those three images to Kubernetes, with command-api
and query-api exposed externally via the Kubernetes Gateway API.

Postgres and NATS JetStream are the platform's two infrastructure dependencies (plan §1). This
chart treats Postgres as always-external (connection info only, via values) and NATS as
optionally bundled (via the official `nats-io/k8s` Helm chart as a dependency) or external.

## Goals

- Deploy command-api, query-api, and projector as Kubernetes Deployments, each independently
  configurable (replicas, resources, image).
- Run the platform's existing `golang-migrate`-based schema migrations automatically on
  install/upgrade, without duplicating the schema-owner list that already lives in the
  `Makefile`.
- Expose command-api and query-api externally via Gateway API `HTTPRoute` resources, attached
  either to a `Gateway` the chart creates itself or to a pre-existing `Gateway` (owned by a
  platform team, another release, etc.) — selectable via values, with optional TLS when the
  chart creates the `Gateway`.
- Never have the chart itself template a Kubernetes `Secret` containing a real credential —
  Postgres connection info and the JWT HMAC secret (if used) are referenced via
  `existingSecret` values, provisioned out of band.
- Optionally deploy NATS JetStream as a chart dependency for a single-command turnkey install;
  support pointing at an external NATS cluster instead.

## Non-goals (v1)

- No Postgres subchart/bundling — always external (per explicit user decision).
- No autoscaling (HPA), PodDisruptionBudget, or NetworkPolicy resources — just `replicas` +
  resource requests/limits in values, consistent with the plan's existing YAGNI stance
  (docs/PLAN.md §13 already defers Kubernetes-manifest detail).
- No path-based routing between command-api and query-api on one hostname — they get separate
  hostnames, avoiding any dependency on Gateway API method-matching support across
  implementations.

## Chart layout

```
deploy/helm/timadorus-platform/
  Chart.yaml              # dependency: nats (https://nats-io.github.io/k8s/helm/charts/), condition nats.enabled
  Chart.lock
  values.yaml
  README.md               # chart-level usage doc: install prerequisites, values reference
  templates/
    _helpers.tpl                  # name/fullname/labels helpers, shared env-var builders
    command-api-deployment.yaml
    command-api-service.yaml
    command-api-httproute.yaml
    query-api-deployment.yaml
    query-api-service.yaml
    query-api-httproute.yaml
    projector-deployment.yaml
    projector-service.yaml        # ClusterIP, for /metrics scraping only — no route
    gateway.yaml                  # Gateway resource, rendered only when gateway.create is true
    migration-job.yaml            # pre-install,pre-upgrade hook Job
    NOTES.txt
```

All templates live flat in `templates/` (no per-service subdirectories) — file name prefixes
(`command-api-*`, `query-api-*`, `projector-*`) provide the grouping instead.

## Components

### command-api / query-api Deployments

- One container each, image configurable via `commandApi.image.{repository,tag,pullPolicy}` /
  `queryApi.image.{repository,tag,pullPolicy}` (default tag falls back to `Chart.AppVersion`).
- Env vars set directly to match `internal/config`: `COMMAND_API_ADDR`/`QUERY_API_ADDR` (from
  `containerPort`, default unchanged at `:8081`/`:8082`), `DATABASE_URL` (via
  `secretKeyRef` → `postgres.existingSecret`/`postgres.secretKey`), `NATS_URL` (command-api
  only — computed from the bundled NATS subchart's service name when `nats.enabled=true`, else
  `nats.externalURL`), and the JWT block: `JWT_JWKS_URL`/`JWT_ISSUER`/`JWT_AUDIENCE` (plain
  values) plus `JWT_HMAC_SECRET` (via `secretKeyRef` → `jwt.hmac.existingSecret`/`.secretKey`,
  only when `jwt.mode: hmac`) / `JWT_HMAC_KEY_ID`.
- `livenessProbe`/`readinessProbe`: HTTP GET `/healthz` / `/readyz` on the container port.
- `resources`, `replicas` from values; no autoscaling.

### projector Deployment

- Same shape minus the JWT block and minus `HTTPAddr`'s public-API role — its HTTP port only
  serves `/healthz`/`/readyz`/`/metrics` (plan §3's config.go comment already documents this).
- `DATABASE_URL` and `NATS_URL` wired the same way as command-api.
- A plain `ClusterIP` Service is created for it (Prometheus scraping / in-cluster health checks
  only) — no `HTTPRoute`, since the projector has no public API (plan §3).

### Services

- `command-api-service` / `query-api-service` / `projector-service`: `ClusterIP`, one port
  matching each component's `HTTPAddr`.

### Gateway + HTTPRoutes

`gateway.create` (bool, default `true`) selects between two mutually exclusive modes:

- **`gateway.create: true`** — `gateway.yaml` templates a `gateway.networking.k8s.io/v1`
  `Gateway` owned by this release:
  - `spec.gatewayClassName`: `{{ required "gateway.gatewayClassName must be set when gateway.create is true — it is cluster-specific (e.g. the installed Gateway API controller's class)" .Values.gateway.gatewayClassName }}`
  - an `http` listener (port 80) always present
  - an optional `https` listener (port 443, `tls.mode: Terminate`, `certificateRefs` from
    `gateway.tls.secretName`) when `gateway.tls.enabled` is true
  - both HTTPRoutes' `parentRefs` point at this chart-owned `Gateway` by name (no
    `namespace`/`sectionName` needed — same release, same namespace, both listeners open to
    routes).
- **`gateway.create: false`** — `gateway.yaml` renders nothing (no `Gateway` resource in this
  release). Both HTTPRoutes' `parentRefs` instead point at an existing `Gateway` described by
  `gateway.existing.name` (required in this mode), `gateway.existing.namespace` (optional,
  defaults to the release namespace — set this when the target `Gateway` lives elsewhere and
  its listeners allow routes `From: All`), and `gateway.existing.sectionName` (optional, targets
  one specific listener on that `Gateway` rather than all of them). `gateway.gatewayClassName`
  and `gateway.tls.*` are ignored in this mode — TLS termination is whatever the existing
  `Gateway`'s owner already configured.
- Either way, `command-api-httproute.yaml` / `query-api-httproute.yaml` each add
  `hostnames: [<component>.<value>]` (`commandApi.route.hostname` / `queryApi.route.hostname` —
  both required values, no sensible default since they're deployment-specific DNS names), a
  single `PathPrefix: /` match, and a `backendRef` to the corresponding Service/port — this part
  is identical in both modes.

### Migration Job

- New `Dockerfile.migrate` (repo root, alongside the existing three Dockerfiles): builds
  `golang-migrate` (same `@v4.19.1` pin the Makefile uses, with the `postgres` build tag),
  copies all 8 schema-owner migration directories into `/migrations/<name>/`, and copies in a
  new `scripts/migrate-up.sh`.
- `scripts/migrate-up.sh` is a new shared script holding the schema-owner list (name →
  migrations path) that the `Makefile`'s `migrate-up`/`migrate-down` targets already inline
  today; the Makefile targets are refactored to call this script instead of duplicating the
  list, so there is exactly one place that enumerates the 8 schema owners. The script takes the
  migrations base directory as an env var (`MIGRATIONS_BASE`, defaulting to the repo-relative
  paths for local `make migrate-up`/`make migrate-down`, and `/migrations` inside the
  container) and a direction argument (`up`/`down`), and loops
  `migrate -database "$DATABASE_URL&x-migrations-table=schema_migrations_$name" -source "file://$MIGRATIONS_BASE/$path" $direction`
  per owner, exactly matching today's Makefile behavior.
- `migration-job.yaml` templates a `batch/v1` `Job` annotated
  `helm.sh/hook: pre-install,pre-upgrade` / `helm.sh/hook-weight: "0"` /
  `helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded`, running the
  `Dockerfile.migrate` image with `DATABASE_URL` from the same `postgres.existingSecret` and
  invoking `migrate-up.sh up`.

### Postgres (external only)

- `values.postgres.existingSecret` (Secret name, must already exist in the release namespace)
  + `values.postgres.secretKey` (key within it holding a full `DATABASE_URL`, e.g.
  `postgres://user:pass@host:5432/db?sslmode=...`).
- No chart-templated Secret, no bundled Postgres.

### NATS (optional dependency)

- `Chart.yaml` dependency: `nats`, version pinned to the chart version verified during
  brainstorming (`2.14.2`), repository `https://nats-io.github.io/k8s/helm/charts/`,
  `condition: nats.enabled`.
- `values.nats.enabled` (default `true`). When enabled, the subchart is configured with
  `config.jetstream.enabled: true` (forced — the platform requires JetStream, confirmed via
  `helm show values nats/nats` during design) via this chart's `values.yaml` passing through
  `nats.config.jetstream.enabled: true` as the default (still overridable, e.g. to tune
  `fileStore`/`memoryStore`/PVC size).
- `NATS_URL` is computed in `_helpers.tpl` as `nats://{{ .Release.Name }}-nats:4222` when
  `nats.enabled` (matches the subchart's own Service naming, confirmed via `helm template`
  during design), or taken verbatim from `values.nats.externalURL` when `nats.enabled: false`.

### Secrets

- `existingSecret` pattern for every credential (Postgres `DATABASE_URL`, JWT HMAC secret) —
  no Secret objects are templated by this chart. JWKS URL/issuer/audience are plain (non-secret)
  values.

## Values schema (illustrative, not exhaustive)

```yaml
commandApi:
  image: { repository: "", tag: "", pullPolicy: IfNotPresent }
  replicas: 1
  resources: {}
  route:
    hostname: ""   # required
queryApi:
  image: { repository: "", tag: "", pullPolicy: IfNotPresent }
  replicas: 1
  resources: {}
  route:
    hostname: ""   # required
projector:
  image: { repository: "", tag: "", pullPolicy: IfNotPresent }
  replicas: 1
  resources: {}
migration:
  image: { repository: "", tag: "", pullPolicy: IfNotPresent }

postgres:
  existingSecret: ""   # required
  secretKey: "DATABASE_URL"

nats:
  enabled: true
  externalURL: ""      # used only when enabled: false
  config:
    jetstream:
      enabled: true

jwt:
  mode: jwks           # "jwks" | "hmac"
  jwksURL: ""
  issuer: ""
  audience: ""
  hmac:
    existingSecret: ""
    secretKey: "JWT_HMAC_SECRET"
    keyID: "dev"

gateway:
  create: true            # true: chart creates its own Gateway; false: attach to an existing one
  gatewayClassName: ""     # required when create: true; cluster-specific
  tls:
    enabled: false         # only used when create: true
    secretName: ""
  existing:
    name: ""               # required when create: false
    namespace: ""          # optional when create: false, defaults to the release namespace
    sectionName: ""        # optional when create: false, targets one listener on the existing Gateway
```

## Testing / verification

- `helm lint deploy/helm/timadorus-platform`.
- `helm template` with a minimal values override (dummy `existingSecret` names, dummy
  hostnames, dummy `gatewayClassName`) — confirm all resources render, `nats.enabled=true` and
  `nats.enabled=false` both render cleanly, `gateway.tls.enabled` both ways render cleanly,
  `gateway.create=true` (no `gateway.existing.*` needed) and `gateway.create=false` (with
  `gateway.existing.name` set, and confirm no `Gateway` resource is rendered) both render
  cleanly.
- `helm dependency update` succeeds and pulls the pinned `nats` chart version.
- Manual: `helm install` against a local cluster (e.g. kind) with a real Postgres reachable via
  a pre-created Secret, confirm the migration Job runs to completion before the three
  Deployments become ready, confirm `/healthz`/`/readyz` pass, confirm an `HTTPRoute` is
  accepted by a real Gateway API controller if one is available in the test environment.
- `make migrate-up`/`make migrate-down` still work unchanged after the Makefile refactor to
  call `scripts/migrate-up.sh` (regression check on the existing local-dev workflow).
