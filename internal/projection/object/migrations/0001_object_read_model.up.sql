CREATE TABLE objects_read_model (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    universe_id UUID NOT NULL,
    is_archived BOOLEAN NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX ON objects_read_model (universe_id);
