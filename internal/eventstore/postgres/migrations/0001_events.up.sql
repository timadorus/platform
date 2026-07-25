CREATE TABLE events (
    global_seq     BIGSERIAL PRIMARY KEY,
    aggregate_id   UUID NOT NULL,
    aggregate_type TEXT NOT NULL,
    version        INT NOT NULL,
    event_type     TEXT NOT NULL,
    payload        JSONB NOT NULL,
    metadata       JSONB NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (aggregate_id, version)
);

CREATE INDEX idx_events_aggregate ON events (aggregate_id, version);
