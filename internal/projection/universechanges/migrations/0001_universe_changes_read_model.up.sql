-- One row per event on Universe/Campaign/Entity/Object/Character, scoped to the Universe it
-- belongs to — powers a per-Universe, polling-based change feed the SPA (or any client) can use
-- to detect a change made by another tab/user/the CLI, without already knowing the specific
-- aggregate to re-check. global_seq is the event store's own already-globally-ordered sequence,
-- reused directly as this table's primary key and as the client's polling cursor — no separate
-- sequence needed. Written by five independent, stateless projectors
-- (internal/projection/universechanges), never by cmd/timadorus-engine. User/Ruleset are
-- unparented and never appear here (see the design spec's own Explicitly Out of Scope).
CREATE TABLE universe_changes_read_model (
    global_seq     BIGINT PRIMARY KEY,
    universe_id    UUID NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id   UUID NOT NULL,
    event_type     TEXT NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL
);
CREATE INDEX ON universe_changes_read_model (universe_id, global_seq);
