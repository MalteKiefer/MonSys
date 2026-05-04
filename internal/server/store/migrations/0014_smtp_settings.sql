-- 0014: split outbound mail into a global SMTP provider (admin-managed) and
-- per-user "email" notification channels that just carry a recipient address.
-- Pre-existing smtp-typed channels migrate to the new model: their host/port/
-- auth bubble up into smtp_settings (first one wins), the channel itself is
-- rewritten to type='email' with config emptied and recipient_email kept.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE smtp_settings (
    id                   SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    host                 TEXT    NOT NULL,
    port                 INTEGER NOT NULL DEFAULT 587 CHECK (port > 0 AND port < 65536),
    username             TEXT    NOT NULL DEFAULT '',
    password             TEXT    NOT NULL DEFAULT '',
    from_address         TEXT    NOT NULL,
    starttls             BOOLEAN NOT NULL DEFAULT TRUE,
    tls                  BOOLEAN NOT NULL DEFAULT FALSE,
    insecure_skip_verify BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by           TEXT
);

ALTER TABLE notification_channels
    ADD COLUMN recipient_email TEXT;

INSERT INTO smtp_settings (
    id, host, port, username, password, from_address,
    starttls, tls, insecure_skip_verify, updated_by
)
SELECT 1,
       COALESCE(config->>'host', ''),
       COALESCE(NULLIF(config->>'port', '')::int, 587),
       COALESCE(config->>'username', ''),
       COALESCE(config->>'password', ''),
       COALESCE(config->>'from', ''),
       COALESCE(NULLIF(config->>'starttls', '')::bool, TRUE),
       COALESCE(NULLIF(config->>'tls', '')::bool, FALSE),
       COALESCE(NULLIF(config->>'insecure_skip_verify', '')::bool, FALSE),
       'migration-0014'
FROM notification_channels
WHERE type = 'smtp' AND COALESCE(config->>'host', '') <> ''
ORDER BY created_at
LIMIT 1
ON CONFLICT (id) DO NOTHING;

UPDATE notification_channels
SET recipient_email = COALESCE(
        config->'to'->>0,
        NULLIF(config->>'to', '')
    ),
    config = '{}'::jsonb,
    type   = 'email'
WHERE type = 'smtp';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notification_channels DROP COLUMN IF EXISTS recipient_email;
DROP TABLE IF EXISTS smtp_settings;
-- +goose StatementEnd
