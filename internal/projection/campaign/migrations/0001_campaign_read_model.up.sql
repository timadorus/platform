CREATE TABLE campaigns_read_model (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    universe_id UUID NOT NULL,
    is_archived BOOLEAN NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX ON campaigns_read_model (universe_id);

CREATE TABLE campaign_gamemasters (
    campaign_id UUID NOT NULL REFERENCES campaigns_read_model(id),
    user_id     UUID NOT NULL,
    PRIMARY KEY (campaign_id, user_id)
);
