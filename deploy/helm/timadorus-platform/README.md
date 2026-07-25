# timadorus-platform Helm chart

Deploys the Timadorus CQRS/ES platform's three binaries (command-api, query-api, projector) to
Kubernetes, running schema migrations automatically and exposing command-api/query-api via the
Gateway API. See `docs/PLAN.md` for the platform architecture this chart deploys.

## Prerequisites

- A Kubernetes cluster with the [Gateway API CRDs](https://gateway-api.sigs.k8s.io/) installed,
  and (unless `gateway.create: false` and you're attaching to an already-working Gateway) a
  Gateway API controller installed and its `GatewayClass` name known.
- A reachable Postgres instance, plus a `Secret` in the release namespace holding a
  `DATABASE_URL` connection string (this chart never creates that Secret — see
  `postgres.existingSecret` below).
- Images for `command-api`, `query-api`, `projector`, and `migrate` built and pushed somewhere
  the cluster can pull from (`Dockerfile.command-api`, `Dockerfile.query-api`,
  `Dockerfile.projector`, `Dockerfile.migrate` at the repo root).

## Install

```bash
helm dependency update deploy/helm/timadorus-platform

helm install my-platform deploy/helm/timadorus-platform \
  --set postgres.existingSecret=my-postgres-secret \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set gateway.gatewayClassName=my-gateway-class \
  --set jwt.jwksURL=https://my-idp.example.com/.well-known/jwks.json
```

### Attaching to an existing Gateway instead of creating one

```bash
helm install my-platform deploy/helm/timadorus-platform \
  --set postgres.existingSecret=my-postgres-secret \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set gateway.create=false \
  --set gateway.existing.name=shared-gateway \
  --set gateway.existing.namespace=gateway-infra \
  --set jwt.jwksURL=https://my-idp.example.com/.well-known/jwks.json
```

### Using an external NATS cluster instead of the bundled one

```bash
  --set nats.enabled=false \
  --set nats.externalURL=nats://nats.my-cluster.svc.cluster.local:4222
```

## Values reference

| Key | Default | Description |
|---|---|---|
| `commandApi.image.repository` / `.tag` / `.pullPolicy` | `timadorus/command-api` / `""` (falls back to `Chart.AppVersion`) / `IfNotPresent` | command-api image |
| `commandApi.replicas` | `1` | command-api replica count |
| `commandApi.route.hostname` | `""` (required) | hostname the command-api `HTTPRoute` matches |
| `queryApi.*` | (mirrors `commandApi.*`) | query-api equivalents |
| `projector.image.*` / `.replicas` | (mirrors `commandApi.*`) | projector has no `route.hostname` — no public API |
| `migration.image.*` | `timadorus/migrate` / ... | image used by the pre-install/pre-upgrade migration Job |
| `postgres.existingSecret` | `""` (required) | name of a pre-existing Secret holding `DATABASE_URL` |
| `postgres.secretKey` | `DATABASE_URL` | key within that Secret |
| `nats.enabled` | `true` | deploy NATS JetStream as a subchart dependency |
| `nats.externalURL` | `""` | used instead when `nats.enabled: false` |
| `jwt.mode` | `jwks` | `jwks` or `hmac` |
| `jwt.issuer` / `.audience` | `""` | always set (both `jwks` and `hmac` modes) |
| `jwt.jwksURL` | `""` | required when `jwt.mode: jwks` |
| `jwt.hmac.existingSecret` / `.secretKey` / `.keyID` | `""` / `JWT_HMAC_SECRET` / `dev` | used when `jwt.mode: hmac` |
| `gateway.create` | `true` | `true`: chart creates its own `Gateway`; `false`: attach to an existing one |
| `gateway.gatewayClassName` | `""` (required when `create: true`) | `GatewayClass` for the chart-owned `Gateway` |
| `gateway.tls.enabled` / `.secretName` | `false` / `""` | optional HTTPS listener on the chart-owned `Gateway` |
| `gateway.existing.name` | `""` (required when `create: false`) | name of the pre-existing `Gateway` to attach to |
| `gateway.existing.namespace` / `.sectionName` | `""` | optional — namespace and section name of the pre-existing `Gateway` (omitted if empty) |
