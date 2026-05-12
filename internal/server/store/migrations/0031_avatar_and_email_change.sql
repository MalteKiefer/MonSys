-- 0031: per-user avatar storage. Email-change confirmation flows through the
-- existing user_action_tokens table (kind "email_change"), so no new table is
-- required there — only the avatar columns on `users`.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS avatar_bytes        BYTEA,
    ADD COLUMN IF NOT EXISTS avatar_content_type TEXT,
    ADD COLUMN IF NOT EXISTS avatar_updated_at   TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users
    DROP COLUMN IF EXISTS avatar_bytes,
    DROP COLUMN IF EXISTS avatar_content_type,
    DROP COLUMN IF EXISTS avatar_updated_at;
-- +goose StatementEnd
