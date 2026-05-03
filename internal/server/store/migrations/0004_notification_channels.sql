-- 0004: notification channels for outbound alerts.
-- Channels are typed (smtp, slack, mattermost, ntfy). Per-type configuration
-- lives in `config` (jsonb) so we don't grow a column every time we add a
-- backend. Sensitive fields (SMTP password, webhook URLs, ntfy tokens) are
-- stored as-is in v1; restrict DB access accordingly. A future migration may
-- introduce envelope encryption.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE notification_channels (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type            TEXT NOT NULL,                       -- smtp | slack | mattermost | ntfy
    name            TEXT NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    config          JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      TEXT,
    last_used_at    TIMESTAMPTZ,
    last_error      TEXT,
    UNIQUE (type, name)
);
CREATE INDEX notification_channels_type_idx ON notification_channels (type);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notification_channels CASCADE;
-- +goose StatementEnd
