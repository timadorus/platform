CREATE TABLE universes_read_model (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    is_archived BOOLEAN NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE universe_creators (
    universe_id UUID NOT NULL REFERENCES universes_read_model(id),
    user_id     UUID NOT NULL,
    PRIMARY KEY (universe_id, user_id)
);
