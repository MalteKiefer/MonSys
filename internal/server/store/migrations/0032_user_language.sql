-- 0032: per-user UI language preference. 'auto' = follow the browser /
-- system locale (default), 'en' / 'de' pin the SPA to that locale.
-- Add new values by widening the CHECK constraint in a follow-up migration.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT 'auto'
        CHECK (language IN ('auto', 'en', 'de'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users
    DROP COLUMN IF EXISTS language;
-- +goose StatementEnd
