-- One generic table across every ruleset-engine data table file (traits.yaml today, more to
-- follow) rather than one bespoke table per file — table shapes vary per file, so `data` is an
-- opaque JSON payload, the same pattern already used for Campaign.configuration and
-- Character.info elsewhere in this platform. Table names are unique WITHIN a Ruleset (the
-- primary key), not globally — a different Ruleset can reuse a table name like "traits" for its
-- own, unrelated rows without collision.
--
-- Not written by a projection.Projector reacting to bus events — timadorus-engine writes it
-- directly at startup from embedded YAML files (see internal/engine/timadorus/tables), since
-- table rows have no event-sourced lifecycle (plan §Context: rule changes are always modeled as
-- new Rulesets, never as edits to an existing one's tables). It lives under internal/projection
-- purely by the same "migrations grouped with the read model they define" convention every other
-- read-model schema already follows, for discoverability by both the writer
-- (cmd/timadorus-engine) and the reader (cmd/query-api).
CREATE TABLE ruleset_tables_read_model (
    ruleset_id UUID NOT NULL,
    table_name TEXT NOT NULL,
    row_key    TEXT NOT NULL,
    data       JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (ruleset_id, table_name, row_key)
);
