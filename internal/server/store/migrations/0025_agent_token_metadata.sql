-- 0025: extend agent_tokens with enrollment metadata.
-- default_tags / default_group_ids are applied to host_tags + host_group_members
-- on the first successful registration; subsequent re-registrations leave the
-- existing membership intact. default_label seeds the host's display label
-- (currently stored as labels->>'host' until a real display_name column lands).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE agent_tokens
    ADD COLUMN default_tags      TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN default_group_ids UUID[] NOT NULL DEFAULT '{}',
    ADD COLUMN default_label     TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE agent_tokens
    DROP COLUMN IF EXISTS default_tags,
    DROP COLUMN IF EXISTS default_group_ids,
    DROP COLUMN IF EXISTS default_label;
-- +goose StatementEnd
