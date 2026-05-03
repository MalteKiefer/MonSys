-- 0009: per-user notification channel ownership.
-- Channels with owner_user_id NULL are admin/system-shared (e.g. SMTP for
-- system mail). Channels with a user id are private to that user; they can
-- still be referenced by rules but only their owner (or any admin) can edit
-- or delete them.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE notification_channels
    ADD COLUMN owner_user_id UUID NULL REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX notification_channels_owner_idx ON notification_channels (owner_user_id);
-- Re-tighten uniqueness so two users can each have a "slack/alerts" channel.
-- The migration may fail here if the existing data has duplicate (type,name)
-- pairs that would now collide — fix that first.
ALTER TABLE notification_channels DROP CONSTRAINT notification_channels_type_name_key;
CREATE UNIQUE INDEX notification_channels_type_name_owner_uq
    ON notification_channels (type, name, COALESCE(owner_user_id, '00000000-0000-0000-0000-000000000000'::uuid));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS notification_channels_type_name_owner_uq;
ALTER TABLE notification_channels ADD CONSTRAINT notification_channels_type_name_key UNIQUE (type, name);
DROP INDEX IF EXISTS notification_channels_owner_idx;
ALTER TABLE notification_channels DROP COLUMN IF EXISTS owner_user_id;
-- +goose StatementEnd
