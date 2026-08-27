# Timadorus Platform

A DDD-flavored CQRS/Event-Sourcing platform in Go, built on [Watermill](https://watermill.io/) +
PostgreSQL + NATS JetStream, with spec-first OpenAPI APIs generated via
[oapi-codegen](https://github.com/oapi-codegen/oapi-codegen).

It manages seven related domain concepts for a tabletop-RPG-style application: **User**,
**Universe**, **Campaign**, **Character**, **Entity**, **Object**, and **Ruleset**. See
[`docs/PLAN.md`](docs/PLAN.md) for the full design and [`docs/adr/`](docs/adr/) for the
individual architecture decisions behind it. A Vue 3 + Tailwind web console for the Game
Master role lives in [`web/`](web/) — see `docs/PLAN.md` §15 and
[`docs/superpowers/specs/2026-08-09-gm-web-spa-design.md`](docs/superpowers/specs/2026-08-09-gm-web-spa-design.md).

## Architecture at a glance

Three independently deployable Go binaries, one Postgres database, one NATS JetStream bus,
plus a static SPA that talks to `command-api`/`query-api` directly from the browser:

```
command-api  --write-->  Postgres (event store + outbox)  --relay-->  NATS JetStream
                                                                            │
query-api  <--read--  Postgres (read models)  <--apply events--  projector
```

- **`command-api`** — validates and appends domain events, embeds the outbox relay that
  publishes them to NATS.
- **`projector`** — subscribes to NATS, applies events to Postgres read-model tables, one
  independent projection per aggregate type.
- **`query-api`** — serves read models straight from Postgres; never touches the event store
  or domain logic.

Every mutating and read endpoint requires a JWT bearer token. Aggregates are never hard-deleted
— only archived (`isArchived` + an idempotent `Archive` command on every aggregate type).

## Prerequisites

- Go 1.26+
- Docker (to run the `kind` cluster used by `make dev-up`, for local Postgres/NATS via
  `docker-compose`, and for the testcontainers-backed integration tests)
- `kind`, `kubectl`, and `helm` (for `make dev-up`/`make dev-down`, the primary local dev flow)
- Node.js 22+ and npm (for the `web/` SPA)

## Quickstart

```sh
make dev-up
```

`dev-up` stands up the **entire platform** — all three Go binaries plus the web SPA pod, fronted
by a real Gateway API controller and a real local OIDC provider — on a Kubernetes cluster: it
targets whatever your current kubeconfig context already reaches, falling back to an existing
`kind` cluster of its own name, and only creating a new `kind` cluster if neither is available.
It installs whatever's missing (cert-manager, the Prometheus Operator, CloudNativePG, NATS
JetStream, the Gateway API CRDs, Traefik as the Gateway API controller, Zitadel as a local OIDC
provider) and deploys the platform itself freshly built from your current code, via the real
Helm chart (`deploy/helm/timadorus-platform`) — the same one used in production and by
`make test-e2e`, into its own `timadorus-dev` namespace/Helm release (isolated from
`test-e2e`'s namespace, though the GatewayClass and NATS JetStream streams are shared
cluster-wide — see below). The web pod is fully configured with real API and OIDC endpoints in
this flow, so it's meant for interactive browser use, not just curl — see the login workflow
below. The cluster also arrives pre-seeded with one User, and a "Timadorus" Ruleset that
`timadorus-engine` auto-registers on startup, so there's something to see right after logging
in. When it's ready, it prints something like:

```
Dev cluster ready. Namespace: timadorus-dev

Open the web UI (one port-forward covers the app and both APIs):
  kubectl port-forward --namespace traefik svc/traefik 8080:80

  http://localhost:8080/

Log in (another terminal — Zitadel needs its own two port-forwards, see below) with:
  username: devuser@timadorus.local
  password: <randomly generated>

Pre-seeded: User "devuser@timadorus.local".
Ruleset "Timadorus" is auto-registered by timadorus-engine on startup.

Zitadel (needed for the login redirect above to resolve):
  kubectl port-forward --namespace zitadel svc/zitadel 8084:8080
  kubectl port-forward --namespace zitadel svc/zitadel-login 8085:3000

Direct API access — fetch a real token via client_credentials, then curl:
  TOKEN=$(curl -s -u <client-id>:<client-secret> -d grant_type=client_credentials -d "scope=openid profile" http://localhost:8084/oauth/v2/token | jq -r .access_token)
  curl http://localhost:8080/api/query/universes -H "Authorization: Bearer $TOKEN"

Per-service access without the shared Gateway (also still available):
  kubectl port-forward --namespace timadorus-dev svc/timadorus-dev-timadorus-platform-command-api 8081:8081
  kubectl port-forward --namespace timadorus-dev svc/timadorus-dev-timadorus-platform-query-api 8082:8082
```

Once it prints its status, three `kubectl port-forward` commands (each run in its own terminal)
give you the full experience: one to Traefik (`http://localhost:8080/` — the web UI, and both
APIs at `/api/command`/`/api/query`, all one origin), one to Zitadel itself
(`http://localhost:8084` — needed for the login redirect to resolve; it can't share Traefik's
origin, see `docs/superpowers/specs/2026-08-10-dev-gateway-oidc-design.md` §2 if you're curious
why), and one to Zitadel's separate login-UI Service (`http://localhost:8085` — Zitadel's
backend redirects the browser here to actually render the login form; see
`docs/superpowers/specs/2026-08-11-zitadel-login-ui-routing-design.md` for why this is a third,
distinct origin). Log in with the printed test-user credentials for a real interactive browser
session. For scripted/curl access instead of the browser, the printed `client_credentials`
command fetches a real token the same way.

When you're done:

```sh
make dev-down
```

`dev-down` only removes what `dev-up` itself installed — if cert-manager/the Prometheus
Operator/CloudNativePG/NATS/Traefik/Zitadel were already on your cluster for some other reason,
they're left running. The GatewayClass and NATS JetStream streams are shared cluster-wide,
though (not namespaced per session), so running `dev-down` — or letting `make test-e2e` finish —
while the other flow is still live can disrupt it: it deletes the shared GatewayClass and purges
shared NATS streams, including the still-live session's own event data.

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

## Configuration

All four binaries are configured entirely via environment variables (`internal/config`):

| Variable | Used by | Default |
|---|---|---|
| `DATABASE_URL` | all | `postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable` |
| `NATS_URL` | command-api, projector, timadorus-engine | `nats://localhost:4222` |
| `COMMAND_API_ADDR` | command-api | `:8081` |
| `QUERY_API_ADDR` | query-api | `:8082` |
| `PROJECTOR_ADDR` | projector (health/readiness/metrics only — no public API) | `:8083` |
| `TIMADORUS_ENGINE_ADDR` | timadorus-engine (health/readiness/metrics only — no public API) | `:8084` |
| `JWT_JWKS_URL` | command-api, query-api | unset — fetches verification keys from an IdP |
| `JWT_HMAC_SECRET` | command-api, query-api | unset — static HS256 secret, dev/test only |
| `JWT_HMAC_KEY_ID` | command-api, query-api | `dev` — must match the `kid` on HMAC-signed test tokens |
| `JWT_ISSUER` / `JWT_AUDIENCE` | command-api, query-api | unset — skips that check if unset |

## Operational endpoints

All four binaries expose (unauthenticated, exempt from OpenAPI schema validation):

- `GET /healthz` — liveness
- `GET /readyz` — readiness (pings the Postgres pool)
- `GET /metrics` — Prometheus metrics (HTTP latency, event-append latency, outbox publish lag,
  per-projection lag, projection outcome counts)

## Testing

```sh
make test          # unit tests + testcontainers-backed integration tests (needs Docker)
```

- Domain unit tests: `internal/domain/*/*_test.go` — given/when/then invariant coverage for
  every aggregate type.
- Integration tests (`testcontainers-go`, spins up real Postgres containers):
  `internal/eventstore/postgres/store_test.go` (append/load, optimistic concurrency,
  `UnitOfWork` atomicity), `internal/projection/universe/projector_test.go` (idempotent
  replay via an in-memory pub/sub).

## Code generation

The OpenAPI specs (`api/command/openapi.yaml`, `api/query/openapi.yaml`) are the source of
truth; server code is generated, not hand-written:

```sh
make generate       # regenerates api/command/gen and api/query/gen
```

CI fails if generated code doesn't match what's committed (i.e. someone edited the spec
without regenerating).

## Docker images

One Dockerfile per binary at the repo root (multi-stage, distroless runtime image):

```sh
docker build -f Dockerfile.command-api      -t timadorus/command-api .
docker build -f Dockerfile.projector        -t timadorus/projector .
docker build -f Dockerfile.query-api        -t timadorus/query-api .
docker build -f Dockerfile.timadorus-engine -t timadorus/timadorus-engine .
docker build -f Dockerfile.web              -t timadorus/web .
```

## Project status

Phases 0–5 of the plan in [`docs/PLAN.md`](docs/PLAN.md) are complete: all seven aggregate
types, the full command → event store → outbox → NATS → projector → read model → query
pipeline, and hardening (structured logging with correlation IDs, Prometheus metrics,
health/readiness endpoints, poison-queue/dead-letter handling, JWT hardening, Dockerfiles,
CI). See `docs/PLAN.md` §12 for the phase-by-phase build history and §13 for known
open questions / deliberately deferred scope (authorization policy beyond JWT validation,
archive cascading, event upcasting, snapshotting, and others).

A Game Master web console (`web/`, see `docs/PLAN.md` §15) is built on top of this platform —
Vue 3 + Tailwind, OIDC PKCE login, full create/manage UI for every aggregate type except
Ruleset (which stays CLI-only), deployed as a fourth container alongside the three Go
binaries.
