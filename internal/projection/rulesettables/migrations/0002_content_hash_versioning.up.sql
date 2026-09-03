-- Table content is otherwise immutable per Ruleset (see 0001's own comment), but real edits to a
-- table's source YAML do happen (e.g. fixing a typo) and previously had no way to reach a cluster
-- that had already synced the old content once — RegisterTables's own
-- ON CONFLICT (ruleset_id, table_name, row_key) DO NOTHING silently discarded the edit forever.
-- Widening the primary key to include a content hash lets an edited row's new content insert as
-- an additional, newer row instead of being dropped, while the old row(s) stay in place as
-- history. The query side (internal/query/rulesettables) always selects the newest row per
-- (ruleset_id, table_name, row_key) by updated_at, so callers still see exactly one row per key —
-- just the latest one.
--
-- One-time, self-healing cost on the first startup after this migration on any cluster that
-- already had rows: every pre-existing row gets content_hash = '' (the DEFAULT below, dropped
-- immediately after backfilling existing rows), which never matches a freshly computed real
-- hash — so RegisterTables's very next run inserts one new, correctly hashed row per existing
-- key, permanently alongside its old content_hash='' placeholder predecessor. Harmless: the
-- query side already picks the newest row by updated_at, so this is invisible to any caller,
-- just a one-time doubling of row count for pre-existing rows.
ALTER TABLE ruleset_tables_read_model ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE ruleset_tables_read_model ALTER COLUMN content_hash DROP DEFAULT;
ALTER TABLE ruleset_tables_read_model DROP CONSTRAINT ruleset_tables_read_model_pkey;
ALTER TABLE ruleset_tables_read_model ADD PRIMARY KEY (ruleset_id, table_name, row_key, content_hash);
CREATE INDEX ruleset_tables_read_model_latest_idx
    ON ruleset_tables_read_model (ruleset_id, table_name, row_key, updated_at DESC);
