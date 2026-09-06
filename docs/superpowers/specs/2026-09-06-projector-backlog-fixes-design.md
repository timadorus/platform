# Projector Backlog Fixes: Connection Budget + Read-Model Rebuild Tool

## Context

`docs/BACKLOG.md`'s "projector" section lists three items. The third (no CI check that generated
API types stay in sync with the OpenAPI specs) is already resolved — the check
(`.github/workflows/ci.yml`'s `web-build` job, "verify generated API clients are up to date" step)
predates the BACKLOG entry claiming it's missing; that entry just needs correcting to `[x] Fixed`,
no code change. This design covers the other two.

## Key Decisions

### 1. `cmd/projector`'s connection pool budget

Mirror `cmd/timadorus-engine/main.go`'s already-established, already-reviewed pattern exactly:
- Add `PoolMaxConns int32` to `config.Projector` (new env var `PROJECTOR_POOL_MAX_CONNS`), reusing
  the existing, already-generic `parsePoolMaxConns(key, def)` helper — no duplication.
- Default to **16**: each `Router.Handle` call holds exactly one pool connection (the
  `universechanges` projectors resolve on the ambient tx rather than acquiring a second, per
  `postgres.Store.Load`'s own design), and there are 12 projectors today, so 16 gives headroom
  above the worst-case simultaneous-cold-start-replay scenario the BACKLOG item describes, plus
  `/readyz`'s own `Ping`.
- Switch `cmd/projector/main.go` from plain `pgxpool.New(ctx, cfg.DatabaseURL)` to
  `pgxpool.ParseConfig` + `pgxpool.NewWithConfig`, matching `cmd/timadorus-engine`'s exact shape.
- Add a "Connection budget" doc comment above the pool construction, mirroring the engine's own.

### 2. Read-model rebuild tool

**A new standalone binary, `cmd/rebuild-read-models`** — not a `cmd/timadorusctl` subcommand.
`timadorusctl` is deliberately HTTP-only (talks to command-api/query-api, never touches Postgres or
NATS directly), matching this codebase's read/write import-graph rule; this tool needs direct
Postgres **and** NATS JetStream admin access, which no existing binary has reason to expose. (A
BACKLOG note has been added flagging the resulting binary-naming inconsistency — `rebuild-read-models`
doesn't get a "ctl" suffix despite being a CLI tool — as a separate, unresolved organizational
question for a future pass; not addressed by this design.)

**The real mechanism, verified against source, not assumed:** resetting a Postgres checkpoint row
alone does **not** cause NATS to redeliver anything. `watermill-nats` binds each projector to a
JetStream *durable consumer* (`internal/bus.NewSubscriber`'s `DurableCalculator`, currently an
inline closure: `prefix + "_" + topic`, e.g. `"universe-read-model_events_universe"`), and that
consumer's own delivery/ack cursor lives entirely inside NATS, independent of our checkpoint table.
A genuine rebuild requires **deleting the durable consumer** (via `nats.go`'s
`JetStreamManager.DeleteConsumer(streamName, durableName)`) so a fresh one gets created and
redelivers the whole retained stream from the start — the checkpoint reset is necessary (so the
idempotency check in `internal/projection/checkpoint` doesn't skip the redelivered old messages)
but not sufficient on its own.

**Deriving the exact (stream, durable) pairs without a separate hardcoded list:** every projector
already exposes `.Name()` and `.Subjects()` (the `internal/projection.Projector` interface), and
the JetStream *stream* name is simply the subject string itself (confirmed in
`watermill-nats`'s `topicInterpreter.ensureStream`: `AddStream(&nats.StreamConfig{Name: topic, ...})`).
So the tool constructs the exact same 12 projector instances `cmd/projector/main.go` already does
(all 12 constructors take no arguments), and for each one computes:
- stream name = `p.Subjects()[0]` (every projector here subscribes to exactly one subject)
- durable name = the same computation `internal/bus.NewSubscriber` already does internally

This guarantees the rebuild tool can never drift out of sync with the real system's naming — no
separate list of 12 projector names to hand-maintain. To make this computation shared (not
duplicated between `internal/bus.NewSubscriber`'s inline closure and this new tool),
**`internal/bus` gains an exported `DurableName(processorName, subject string) string` function**,
and `NewSubscriber`'s `DurableCalculator` becomes a one-line wrapper calling it.

**Why the tool requires `cmd/projector` to already be stopped:** deleting a durable consumer that a
live, bound `nats.go` subscription is actively reading from has undefined behavior in the
underlying client — the safe contract is "stop the consumer of that data, then mutate it, then
restart." The tool does not attempt to detect or coordinate with a running `cmd/projector` process
(no reliable way to do so without adding new coordination machinery this fix doesn't need); it
prints a loud warning and requires an explicit confirmation instead.

> **Superseded by the final-review fix wave.** There *is* a reliable check, needing no new
> coordination machinery: `nats.JetStreamManager.ConsumerInfo(stream, durable).PushBound` is true
> exactly while a push subscription — which is what watermill's subscriber opens — is bound to the
> consumer. The tool now calls it before deleting each consumer and aborts if anything is still
> bound, so the operator's typed confirmation is verified rather than merely trusted. See
> `internal/rebuildreadmodels.ConsumerPushBound`. The warning and the confirmation prompt stay;
> the check is an additional guard, not a replacement.

**Two-phase design, matching the BACKLOG item's own required ordering (bases before change-feed):**

```
rebuild-read-models --confirm
```
1. Refuses to run without `--confirm`.
2. Prints: "cmd/projector MUST already be stopped. Continue? [y/N]" — requires typed `y`.
3. Captures `target := SELECT MAX(global_seq) FROM events`.
4. For each of the 7 base projectors (`universe-read-model`, `campaign-read-model`,
   `entity-read-model`, `character-read-model`, `object-read-model`, `ruleset-read-model`,
   `user-read-model`): `DeleteConsumer(stream, durable)`, then `checkpoint.Set(tx, name, 0)`.
5. Prints: "Base projectors reset. Start cmd/projector now, then re-run with
   `--phase=change-feed --target-seq={target}` once caught up. Checking every 5s..." and polls
   `projection_checkpoints` itself, printing live progress, until all 7 have reached `target` (or
   the operator interrupts it — it's just a progress display, not load-bearing). `target` is
   printed explicitly, not stashed in an implicit marker file — a destructive tool's phase-2
   invocation should be a plain, explicit, copy-pasteable command with no hidden
   directory-dependent state.

```
rebuild-read-models --confirm --phase=change-feed --target-seq=<value from step 5>
```
6. Re-checks the 7 base projectors' current checkpoint values against the explicitly-passed
   `--target-seq` — refuses to proceed if any base projector hasn't caught up, to prevent an
   operator from jumping ahead by mistake.
7. Same warning/confirmation as step 2.
8. For each of the 5 `universe-changes-*` projectors: `DeleteConsumer` + `checkpoint.Set(tx, name, 0)`.
9. Prints: "Change-feed projectors reset. Restart cmd/projector now."

> **Superseded by the final-review fix wave — the `target` in steps 3, 5 and 6 above is a design
> defect.** `MAX(global_seq)` over the whole `events` table is a *watermark*, not a target any
> single projector can reach. A projector's checkpoint only ever advances from messages on its own
> subject, and the outbox relay publishes each event to exactly one subject chosen by its aggregate
> type, so a fully caught-up projector's checkpoint converges to the maximum `global_seq` among
> events of *its own* aggregate type. Unless the very last event in the table happens to belong to
> that type, that is strictly below the whole-table maximum — so at most one of the 7 base
> projectors could ever satisfy the target as specified. In practice step 5 polled forever on a
> rebuild that had already finished, and step 6 refused to proceed on any real workload.
>
> The tool now captures the whole-table maximum once as a *bound* (still the single value printed
> and passed as `--target-seq`, so the operator-facing UX is unchanged) and derives a per-projector
> target from it: `MAX(global_seq) WHERE aggregate_type = <the projector's own type> AND global_seq
> <= watermark`. Steps 5 and 6 both compare against that per-projector target. See
> `internal/rebuildreadmodels.ComputeTargets`, `MaxGlobalSeqForAggregateType`, `VerifyCaughtUp`,
> and `bus.AggregateTypeFromSubject`; `TestComputeTargets_UnequalEventCountsPerAggregateType`
> pins the behaviour against a workload with deliberately unequal per-type event counts.

## Testing Strategy

- **Go unit tests** (`cmd/rebuild-read-models`'s own package, or a small internal helper package it
  delegates to — e.g. `internal/rebuildreadmodels`): against a real `testcontainers` Postgres
  (matching this codebase's established pattern), seed `projection_checkpoints` and `events` rows,
  test the checkpoint-reset SQL and the "poll until all N reach target" logic directly, without
  needing NATS at all for this half.
- **A focused NATS JetStream test** (new test infrastructure — `internal/bus` currently has no test
  file): connect to a real NATS instance (this project's CI already has NATS available via the dev
  cluster tooling, but a plain `nats-server` binary or Docker container is simplest for a unit
  test), create a stream + durable consumer, publish and ack a message, call the new
  `DeleteConsumer`-based reset logic, recreate the consumer, and confirm the message is redelivered
  — proving the actual mechanism this tool exists for, not just that the Go code compiles.
- **No real-cluster e2e**: this is an offline maintenance tool that requires stopping a running
  service, which doesn't fit the existing e2e suite's "everything running" model. A manual runbook
  verification (documented in the tool's own `--help` output and a short doc comment on `main`) is
  the appropriate check here instead.

## Out of Scope

- Resolving the `cmd/` binary naming/organization question this design surfaces — tracked as its
  own, separate, already-added BACKLOG item for a future pass.
- Any change to `cmd/timadorusctl` itself.
- Automatically detecting whether `cmd/projector` is actually stopped — the tool trusts the
  operator's confirmation.
