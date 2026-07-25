CREATE TABLE projection_dead_letters (
    id              BIGSERIAL PRIMARY KEY,
    projection_name TEXT NOT NULL,
    message_uuid    TEXT NOT NULL,
    global_seq      BIGINT,
    aggregate_id    UUID,
    aggregate_type  TEXT,
    event_type      TEXT,
    envelope        JSONB NOT NULL,
    error           TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON projection_dead_letters (projection_name);
