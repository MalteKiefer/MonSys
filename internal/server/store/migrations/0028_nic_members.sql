-- 0028: surface bridge / bond topology on nics.
-- The agent now reads /sys/class/net/<name>/{brif,bonding/slaves,master} and
-- reports member interfaces (for bridge/bond masters) plus bridge_master (for
-- enslaved NICs). Both fields are optional — non-bridge / non-bond NICs keep
-- the empty defaults.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE nics ADD COLUMN IF NOT EXISTS members       TEXT[] NOT NULL DEFAULT '{}'::text[];
ALTER TABLE nics ADD COLUMN IF NOT EXISTS bridge_master TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE nics DROP COLUMN IF EXISTS members;
ALTER TABLE nics DROP COLUMN IF EXISTS bridge_master;
-- +goose StatementEnd
