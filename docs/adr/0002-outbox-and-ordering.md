# 0002: Transactional outbox + serial relay for event delivery ordering

## Status
Accepted

## Context
Events must move from the Postgres event store to NATS JetStream without a dual-write gap
(losing/duplicating events between the two systems), and per-aggregate event order must be
preserved for projections to be correct.

## Decision
- Every `Append` writes the event row and its outbox row in the same Postgres transaction
  (transactional outbox pattern) — eliminates the dual-write problem.
- A single **active** outbox relay (elected via `pg_advisory_lock`, see `internal/outbox`)
  polls the outbox table in `global_seq` order and publishes to NATS JetStream on
  `events.<aggregate_type>` subjects, one subject per aggregate type.
- Each projection's JetStream consumer processes messages **serially** (one goroutine,
  in-order ack).

## Consequences
Strict single-writer publish + serial per-projection consumption trivially preserves
per-aggregate order at the cost of some throughput. Sharding/parallel publish is an explicit
future scaling concern, not built now (see plan §13).
