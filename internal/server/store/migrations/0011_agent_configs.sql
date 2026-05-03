-- 0011: server-managed agent configuration with three scopes:
--   global  — single row, applies to every agent
--   group   — one row per host_groups.id, applies to members
--   host    — one row per hosts.id, applies to that single host
--
-- Resolution order at fetch time: host overrides any group, group overrides
-- global, missing keys fall back to the agent's compiled defaults.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE agent_configs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope           TEXT NOT NULL CHECK (scope IN ('global', 'group', 'host')),
    -- For scope=global, target_id IS NULL. For group/host it points at the
    -- corresponding row. Cascades on delete so rows clean up automatically.
    target_id       UUID,
    config          JSONB NOT NULL DEFAULT '{}'::jsonb,
    description     TEXT,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by      TEXT,
    UNIQUE (scope, target_id)
);
CREATE INDEX agent_configs_scope_idx ON agent_configs (scope);

-- The two soft FKs (we can't do composite REFERENCES because target_id is
-- nullable). Use triggers if you need stricter referential integrity.
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO agent_configs (scope, target_id, config, description, updated_by)
VALUES (
    'global',
    NULL,
    '{
        "interval_seconds": 15,
        "packages": {"enabled": true, "update_check_interval": "30m"},
        "quiet_hours": {"enabled": false}
    }'::jsonb,
    'Default global agent config (seeded by migration)',
    'system'
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_configs CASCADE;
-- +goose StatementEnd
