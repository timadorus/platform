CREATE TABLE users_read_model (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    is_archived BOOLEAN NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ NOT NULL
);
