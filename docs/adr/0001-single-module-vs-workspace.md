# 0001: Single Go module, not a workspace

## Status
Accepted

## Context
The platform ships three binaries (`command-api`, `projector`, `query-api`) sharing a large
amount of `internal/` code (the event-sourcing framework, domain packages, projection
framework). A Go workspace (`go.work`) with separate modules per binary was considered.

## Decision
Use a single Go module for the whole repository. `internal/` visibility already prevents
binaries from depending on each other's private details, and all three binaries must stay in
lockstep with the shared event schema and deploy from the same commit — a workspace's value
(independently versioned, separately releasable modules) doesn't apply here.

## Consequences
Revisit only if the event-sourcing framework is ever extracted as a standalone, separately
versioned library for use outside this repository.
