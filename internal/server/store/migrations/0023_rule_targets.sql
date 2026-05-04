-- 0023: per-rule host/tag/group targeting.
-- Empty arrays preserve the historical "applies to every host" behaviour;
-- the alerts engine treats all-empty as a wildcard.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE notification_rules
    ADD COLUMN target_host_ids  UUID[]  NOT NULL DEFAULT '{}',
    ADD COLUMN target_tags      TEXT[]  NOT NULL DEFAULT '{}',
    ADD COLUMN target_group_ids UUID[]  NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notification_rules
    DROP COLUMN IF EXISTS target_host_ids,
    DROP COLUMN IF EXISTS target_tags,
    DROP COLUMN IF EXISTS target_group_ids;
-- +goose StatementEnd
