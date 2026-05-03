-- 0007: notification rules + alert history.
-- A rule connects a trigger condition to one or more channels. Rules are
-- evaluated by the alerts engine; alert_history records every fire so we can
-- throttle ("don't re-fire within N seconds") and surface a UI of recent alerts.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE notification_rules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    condition_type  TEXT NOT NULL,                       -- host_offline | monitor_failed | cert_expiring | login_failed_threshold | security_updates_pending
    condition_params JSONB NOT NULL DEFAULT '{}'::jsonb,
    channel_ids     UUID[] NOT NULL DEFAULT '{}',
    severity        TEXT NOT NULL DEFAULT 'warning',     -- info | warning | critical
    throttle_sec    INT NOT NULL DEFAULT 600,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE alert_history (
    id              BIGSERIAL PRIMARY KEY,
    at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    rule_id         UUID REFERENCES notification_rules(id) ON DELETE SET NULL,
    rule_name       TEXT,
    severity        TEXT NOT NULL,
    subject         TEXT NOT NULL,
    body            TEXT NOT NULL,
    -- Dedup key: rules use this together with throttle_sec to suppress repeats.
    dedup_key       TEXT NOT NULL,
    delivered_to    TEXT[],                              -- channel names that succeeded
    delivery_errors JSONB NOT NULL DEFAULT '{}'::jsonb   -- { "channel name": "error", ... }
);
CREATE INDEX alert_history_at_idx ON alert_history (at DESC);
CREATE INDEX alert_history_rule_at_idx ON alert_history (rule_id, at DESC);
CREATE INDEX alert_history_dedup_idx ON alert_history (dedup_key, at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS alert_history CASCADE;
DROP TABLE IF EXISTS notification_rules CASCADE;
-- +goose StatementEnd
