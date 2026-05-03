-- 0010: host tags + host groups + membership; monitor targeting columns.
--
-- Tags are simple lowercase text labels (a-z 0-9 _ -) attached to many hosts.
-- Groups are named host collections with explicit membership. Monitors gain
-- optional target_tags / target_group_ids that act as metadata for now —
-- future rule-engine work will filter by them.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE host_tags (
    host_id      UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    tag          TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (host_id, tag)
);
CREATE INDEX host_tags_tag_idx ON host_tags (tag);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE host_groups (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL UNIQUE,
    description  TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by   TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE host_group_members (
    group_id     UUID NOT NULL REFERENCES host_groups(id) ON DELETE CASCADE,
    host_id      UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    added_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, host_id)
);
CREATE INDEX host_group_members_host_idx ON host_group_members (host_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE monitors
    ADD COLUMN target_tags      TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN target_group_ids UUID[] NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE monitors DROP COLUMN IF EXISTS target_group_ids;
ALTER TABLE monitors DROP COLUMN IF EXISTS target_tags;
DROP TABLE IF EXISTS host_group_members CASCADE;
DROP TABLE IF EXISTS host_groups CASCADE;
DROP TABLE IF EXISTS host_tags CASCADE;
-- +goose StatementEnd
