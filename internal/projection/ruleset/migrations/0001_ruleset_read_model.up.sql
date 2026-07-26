CREATE TABLE rulesets_read_model (
    id             UUID PRIMARY KEY,
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    reference_urls TEXT[] NOT NULL DEFAULT '{}',
    is_archived    BOOLEAN NOT NULL DEFAULT false,
    updated_at     TIMESTAMPTZ NOT NULL
);
