CREATE TABLE characters_read_model (
    id             UUID PRIMARY KEY,
    name           TEXT NOT NULL,
    campaign_id    UUID NOT NULL,
    entity_id      UUID NOT NULL,
    player_user_id UUID NOT NULL,
    is_archived    BOOLEAN NOT NULL DEFAULT false,
    updated_at     TIMESTAMPTZ NOT NULL
);
CREATE INDEX ON characters_read_model (campaign_id);
CREATE INDEX ON characters_read_model (player_user_id);
