-- 0030: rule groups. A group_id is a stable identifier shared by N rows
-- in notification_rules that represent one logical rule in the UI (e.g. a
-- single "Production critical" rule with sub-conditions on CPU, RAM, disk).
-- Existing rules retain NULL → they are standalone rules.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE notification_rules
    ADD COLUMN IF NOT EXISTS group_id UUID;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_notification_rules_group_id
    ON notification_rules (group_id) WHERE group_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_notification_rules_group_id;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE notification_rules DROP COLUMN IF EXISTS group_id;
-- +goose StatementEnd
