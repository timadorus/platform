CREATE TABLE outbox (
    id             BIGSERIAL PRIMARY KEY,
    global_seq     BIGINT NOT NULL REFERENCES events (global_seq),
    aggregate_id   UUID NOT NULL,
    aggregate_type TEXT NOT NULL,
    version        INT NOT NULL,
    event_type     TEXT NOT NULL,
    payload        JSONB NOT NULL,
    metadata       JSONB NOT NULL,
    published_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_outbox_unpublished ON outbox (id) WHERE published_at IS NULL;
