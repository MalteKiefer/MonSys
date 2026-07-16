-- 0034: TOTP replay protection.
--
-- AUDIT-2026-07-16 M7 / ASVS V2.8.4: record the highest RFC 6238 time-step a
-- user's TOTP code has been accepted for, so a code observed within its skew
-- window cannot be replayed. 0 means "none consumed yet".

-- +goose Up
-- +goose StatementBegin
ALTER TABLE user_totp ADD COLUMN IF NOT EXISTS last_used_step BIGINT NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE user_totp DROP COLUMN IF EXISTS last_used_step;
-- +goose StatementEnd
