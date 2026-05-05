-- 0024: per-rule repeat reminders + resolve-notification opt-out.
-- repeat_interval_sec = 0 keeps the historical "fire once per outage"
-- behaviour. >= 60 enforces a minimum cadence so misconfigured rules cannot
-- spam channels at sub-minute intervals (the periodic ticker runs every 60s
-- anyway, so values below that floor would just round up).
-- notify_on_resolve toggles the all-clear dispatch path; resolved_at is
-- still stamped either way so dashboards stay accurate.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE notification_rules
    ADD COLUMN repeat_interval_sec INT     NOT NULL DEFAULT 0
        CHECK (repeat_interval_sec = 0 OR (repeat_interval_sec >= 60 AND repeat_interval_sec <= 86400)),
    ADD COLUMN notify_on_resolve   BOOLEAN NOT NULL DEFAULT TRUE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notification_rules
    DROP COLUMN IF EXISTS repeat_interval_sec,
    DROP COLUMN IF EXISTS notify_on_resolve;
-- +goose StatementEnd
