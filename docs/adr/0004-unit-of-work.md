# 0004: Postgres-specific UnitOfWork for cross-aggregate creation

## Status
Accepted

## Context
Creating a Character must also create its paired Entity (see plan §4.4). Both are separate
aggregate types with independent event streams, but they share the same physical Postgres
database as every other aggregate type, so there's no need to pay for saga-style eventual
consistency — a single ACID transaction spanning both `Append` calls is simpler and strictly
stronger.

## Decision
`internal/eventstore/postgres.UnitOfWork` begins a `pgx.Tx` and stashes it in the returned
context (`WithTx`, `tx.go`). `Store.Append`, when called with that context, joins the ambient
transaction instead of opening its own — `internal/eventsourcing.Repository[T]` and
`EventStore` themselves need no changes.

Originally sketched as living in `internal/eventsourcing` (storage-agnostic package), it was
moved to `internal/eventstore/postgres` during implementation: `UnitOfWork` is inherently
Postgres-specific (it wraps `pgx.Tx`), and keeping it there avoids a real import cycle
(`eventsourcing` -> `eventstore/postgres` -> `eventsourcing`, since `Store` implements
`eventsourcing.EventStore`). Command services that need cross-aggregate atomicity (e.g.
`internal/command/character`) import `eventstore/postgres.UnitOfWork` directly — an
application-layer dependency on a specific infrastructure concern, not a domain-layer
violation.

## Consequences
Single-aggregate command services never construct a `UnitOfWork`; `Append` behaves
identically with or without an ambient transaction, so this is a zero-change addition for
every existing call site.
