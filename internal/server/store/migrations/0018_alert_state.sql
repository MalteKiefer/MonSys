-- 0018: track open alert state so the engine can dispatch resolved-flavored
-- notifications when the underlying condition clears.
-- alert_history stays append-only and continues to record every fire; this
-- table layers a small "currently in alert" view keyed by dedup_key on top.
-- The partial index on resolved_at IS NULL keeps "what's still open?" scans
-- cheap regardless of how big the resolved tail grows.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE alert_state (
    dedup_key       TEXT PRIMARY KEY,
    rule_id         UUID NOT NULL REFERENCES notification_rules(id) ON DELETE CASCADE,
    host_id         UUID,           -- nullable; not all rules are host-scoped
    monitor_id      UUID,
    severity        TEXT NOT NULL,
    subject         TEXT NOT NULL,
    channel_ids     UUID[] NOT NULL,
    opened_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_fired_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at     TIMESTAMPTZ
);
CREATE INDEX alert_state_resolved_idx ON alert_state (resolved_at) WHERE resolved_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS alert_state CASCADE;
-- +goose StatementEnd
