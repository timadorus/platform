-- Not safely reversible once more than one content_hash version of a row exists (would violate
-- the original (ruleset_id, table_name, row_key) primary key) — provided for symmetry/CI
-- dev-reset use only. Keeps only the newest row per key (by updated_at, then content_hash to
-- break an exact tie deterministically) before reverting the schema.
DELETE FROM ruleset_tables_read_model a
USING ruleset_tables_read_model b
WHERE a.ruleset_id = b.ruleset_id
  AND a.table_name = b.table_name
  AND a.row_key = b.row_key
  AND (a.updated_at, a.content_hash) < (b.updated_at, b.content_hash);
DROP INDEX IF EXISTS ruleset_tables_read_model_latest_idx;
ALTER TABLE ruleset_tables_read_model DROP CONSTRAINT ruleset_tables_read_model_pkey;
ALTER TABLE ruleset_tables_read_model ADD PRIMARY KEY (ruleset_id, table_name, row_key);
ALTER TABLE ruleset_tables_read_model DROP COLUMN content_hash;
