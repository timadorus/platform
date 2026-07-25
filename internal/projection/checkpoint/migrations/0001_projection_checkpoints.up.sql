CREATE TABLE projection_checkpoints (
    projection_name TEXT PRIMARY KEY,
    last_global_seq BIGINT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
