-- 0021: database hardening pass.
--
-- 1. audit_log: previously a plain table with no retention. Convert to a
--    Timescale hypertable on `at` (extending the PK from id to (id, at) so
--    create_hypertable does not refuse the migrate_data step — same pattern
--    as 0016 used for alert_history). Then attach a 730-day retention policy:
--    two years is plenty for a security-audit trail and keeps the table from
--    growing forever.
-- 2. alert_state.rule_id partial index: 0020 makes rule_id nullable, so a
--    plain index would carry NULLs we never look up. The partial index keeps
--    FK-lookup-on-delete fast without paying for them.
-- 3. CHECK constraints on notification_settings.quiet_days /
--    quiet_start / quiet_end: the API layer already validates these, but
--    direct-SQL writes (psql, ad-hoc tooling) could put the system into a
--    nonsensical state. CHECKs make the schema itself the source of truth.
-- 4. CHECK on smtp_settings.from_address: same rationale; an empty or
--    obviously-malformed from address breaks every outbound mail.

-- +goose Up

-- audit_log: extend PK with the time column, then hypertable + 730d retention.
-- +goose StatementBegin
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_pkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log ADD PRIMARY KEY (id, at);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT create_hypertable('audit_log', 'at', if_not_exists => TRUE, migrate_data => TRUE);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT add_retention_policy('audit_log', INTERVAL '730 days', if_not_exists => TRUE);
-- +goose StatementEnd

-- alert_state: partial index for FK lookups on rule deletion.
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS alert_state_rule_idx ON alert_state(rule_id) WHERE rule_id IS NOT NULL;
-- +goose StatementEnd

-- notification_settings: domain CHECKs so direct-SQL writes can't poison the row.
-- +goose StatementBegin
ALTER TABLE notification_settings
    ADD CONSTRAINT chk_quiet_days_range
    CHECK (quiet_days <@ ARRAY[0,1,2,3,4,5,6]::SMALLINT[]);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE notification_settings
    ADD CONSTRAINT chk_quiet_start_format
    CHECK (quiet_start ~ '^([01]?[0-9]|2[0-3]):[0-5][0-9]$' OR quiet_start = '');
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE notification_settings
    ADD CONSTRAINT chk_quiet_end_format
    CHECK (quiet_end ~ '^([01]?[0-9]|2[0-3]):[0-5][0-9]$' OR quiet_end = '');
-- +goose StatementEnd

-- smtp_settings: from_address must be non-empty and contain '@'.
-- +goose StatementBegin
ALTER TABLE smtp_settings
    ADD CONSTRAINT chk_from_address_nonempty
    CHECK (char_length(from_address) > 0 AND from_address LIKE '%@%');
-- +goose StatementEnd

-- +goose Down
-- The audit_log hypertable conversion is intentionally NOT reverted —
-- IRREVERSIBLE: hypertable conversion + PK change is not reverted by goose down
-- (same stance as 0016). We only roll back the retention policy, the partial
-- index, and the CHECK constraints.
-- +goose StatementBegin
SELECT remove_retention_policy('audit_log', if_exists => TRUE);
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS alert_state_rule_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE notification_settings DROP CONSTRAINT IF EXISTS chk_quiet_days_range;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE notification_settings DROP CONSTRAINT IF EXISTS chk_quiet_start_format;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE notification_settings DROP CONSTRAINT IF EXISTS chk_quiet_end_format;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE smtp_settings DROP CONSTRAINT IF EXISTS chk_from_address_nonempty;
-- +goose StatementEnd
