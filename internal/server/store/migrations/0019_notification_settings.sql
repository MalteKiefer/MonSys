-- 0019: server-side global quiet hours. Singleton row keyed on id=1, the same
-- pattern as smtp_settings. When the configured window is active, the alerts
-- engine still records the alert in alert_history (audit trail intact) but
-- skips dispatching to channels.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE notification_settings (
    id               SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    quiet_enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    quiet_start      TEXT NOT NULL DEFAULT '22:00',  -- HH:MM, server-local
    quiet_end        TEXT NOT NULL DEFAULT '06:00',
    quiet_days       SMALLINT[] NOT NULL DEFAULT '{0,1,2,3,4,5,6}', -- 0=Sun..6=Sat
    quiet_tz         TEXT NOT NULL DEFAULT 'UTC',
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by       TEXT
);
INSERT INTO notification_settings (id) VALUES (1) ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notification_settings;
-- +goose StatementEnd
