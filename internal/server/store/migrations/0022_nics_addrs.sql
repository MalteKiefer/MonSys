-- 0022: persist NIC IPv4 + IPv6 addresses captured during inventory.
-- gopsutil returns these in InterfaceStat.Addrs but the agent dropped them
-- pre-0022, so the UI only ever showed name/MAC/speed.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE nics ADD COLUMN IF NOT EXISTS addrs TEXT[] NOT NULL DEFAULT '{}'::text[];
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE nics DROP COLUMN IF EXISTS addrs;
-- +goose StatementEnd
