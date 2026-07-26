# Timadorus Platform

A DDD-flavored CQRS/Event-Sourcing platform in Go, built on [Watermill](https://watermill.io/) +
PostgreSQL + NATS JetStream, with spec-first OpenAPI APIs generated via
[oapi-codegen](https://github.com/oapi-codegen/oapi-codegen).

It manages seven related domain concepts for a tabletop-RPG-style application: **User**,
**Universe**, **Campaign**, **Character**, **Entity**, **Object**, and **Ruleset**. See
[`docs/PLAN.md`](docs/PLAN.md) for the full design and [`docs/adr/`](docs/adr/) for the
individual architecture decisions behind it.

## Architecture at a glance

Three independently deployable binaries, one Postgres database, one NATS JetStream bus:

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
- Docker (for local Postgres/NATS via `docker-compose`, and for the testcontainers-backed
  integration tests)

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
```

With no `JWT_JWKS_URL`/`JWT_HMAC_SECRET` configured, both APIs fall back to a well-known,
loudly-logged **insecure dev HMAC secret** so they work out of the box locally — never set
this up in a real deployment (see `internal/auth`).

## Configuration

All three binaries are configured entirely via environment variables (`internal/config`):

| Variable | Used by | Default |
|---|---|---|
| `DATABASE_URL` | all | `postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable` |
| `NATS_URL` | command-api, projector | `nats://localhost:4222` |
| `COMMAND_API_ADDR` | command-api | `:8081` |
| `QUERY_API_ADDR` | query-api | `:8082` |
| `PROJECTOR_ADDR` | projector (health/readiness/metrics only — no public API) | `:8083` |
| `JWT_JWKS_URL` | command-api, query-api | unset — fetches verification keys from an IdP |
| `JWT_HMAC_SECRET` | command-api, query-api | unset — static HS256 secret, dev/test only |
| `JWT_HMAC_KEY_ID` | command-api, query-api | `dev` — must match the `kid` on HMAC-signed test tokens |
| `JWT_ISSUER` / `JWT_AUDIENCE` | command-api, query-api | unset — skips that check if unset |

## Operational endpoints

All three binaries expose (unauthenticated, exempt from OpenAPI schema validation):

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
docker build -f Dockerfile.command-api -t timadorus/command-api .
docker build -f Dockerfile.projector   -t timadorus/projector .
docker build -f Dockerfile.query-api   -t timadorus/query-api .
```

## Project status

Phases 0–5 of the plan in [`docs/PLAN.md`](docs/PLAN.md) are complete: all seven aggregate
types, the full command → event store → outbox → NATS → projector → read model → query
pipeline, and hardening (structured logging with correlation IDs, Prometheus metrics,
health/readiness endpoints, poison-queue/dead-letter handling, JWT hardening, Dockerfiles,
CI). See `docs/PLAN.md` §12 for the phase-by-phase build history and §13 for known
open questions / deliberately deferred scope (authorization policy beyond JWT validation,
archive cascading, event upcasting, snapshotting, and others).
