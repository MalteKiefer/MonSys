-- 0033: align PII-bearing table retention with the published privacy policy.
--
-- AUDIT-2026-07-16 M9: docs/PRIVACY.md states a 90-day retention for login
-- events, alert history, and the audit log, but earlier migrations attached
-- 180 / 365 / 730-day policies respectively. All three tables hold PII
-- (usernames, source IPs, actor emails), so the enforced policy must not
-- outlive the published statement. Re-point each policy to 90 days.
--
-- add_retention_policy is idempotent on (hypertable) but will NOT change the
-- interval of an existing policy, so each table is removed then re-added.

-- +goose Up

-- +goose StatementBegin
SELECT remove_retention_policy('login_events', if_exists => TRUE);
-- +goose StatementEnd
-- +goose StatementBegin
SELECT add_retention_policy('login_events', INTERVAL '90 days', if_not_exists => TRUE);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT remove_retention_policy('alert_history', if_exists => TRUE);
-- +goose StatementEnd
-- +goose StatementBegin
SELECT add_retention_policy('alert_history', INTERVAL '90 days', if_not_exists => TRUE);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT remove_retention_policy('audit_log', if_exists => TRUE);
-- +goose StatementEnd
-- +goose StatementBegin
SELECT add_retention_policy('audit_log', INTERVAL '90 days', if_not_exists => TRUE);
-- +goose StatementEnd

-- +goose Down

-- Restore the pre-0033 intervals (180 / 365 / 730 days).
-- +goose StatementBegin
SELECT remove_retention_policy('login_events', if_exists => TRUE);
-- +goose StatementEnd
-- +goose StatementBegin
SELECT add_retention_policy('login_events', INTERVAL '180 days', if_not_exists => TRUE);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT remove_retention_policy('alert_history', if_exists => TRUE);
-- +goose StatementEnd
-- +goose StatementBegin
SELECT add_retention_policy('alert_history', INTERVAL '365 days', if_not_exists => TRUE);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT remove_retention_policy('audit_log', if_exists => TRUE);
-- +goose StatementEnd
-- +goose StatementBegin
SELECT add_retention_policy('audit_log', INTERVAL '730 days', if_not_exists => TRUE);
-- +goose StatementEnd
