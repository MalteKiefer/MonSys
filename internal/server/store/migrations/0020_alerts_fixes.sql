-- 0020: relax alert_state.rule_id so deleting a rule no longer wipes open
-- alert_state rows. With ON DELETE CASCADE (0018) the engine silently lost
-- in-flight incidents the moment an admin tweaked a rule; with SET NULL the
-- open record sticks around so the next tick can still dispatch the
-- all-clear, just without a rule_id back-reference. The column also has to
-- become NULLable for SET NULL to be valid.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE alert_state
    DROP CONSTRAINT IF EXISTS alert_state_rule_id_fkey,
    ADD CONSTRAINT alert_state_rule_id_fkey
        FOREIGN KEY (rule_id) REFERENCES notification_rules(id) ON DELETE SET NULL;
ALTER TABLE alert_state ALTER COLUMN rule_id DROP NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE alert_state ALTER COLUMN rule_id SET NOT NULL;
ALTER TABLE alert_state
    DROP CONSTRAINT IF EXISTS alert_state_rule_id_fkey,
    ADD CONSTRAINT alert_state_rule_id_fkey
        FOREIGN KEY (rule_id) REFERENCES notification_rules(id) ON DELETE CASCADE;
-- +goose StatementEnd
