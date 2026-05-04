-- 0015: pick a sane default for notification_rules.throttle_sec.
-- A throttle of 0 had been interpreted by the engine as "no throttle", which
-- made sustained outages flood the operator's inbox. Switch the column default
-- to 300s and rewrite existing zero rows; the engine now also treats 0 as
-- "use the default" so already-deployed rows behave correctly even before this
-- migration runs.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE notification_rules ALTER COLUMN throttle_sec SET DEFAULT 300;
UPDATE notification_rules SET throttle_sec = 300 WHERE throttle_sec = 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notification_rules ALTER COLUMN throttle_sec SET DEFAULT 600;
-- +goose StatementEnd
