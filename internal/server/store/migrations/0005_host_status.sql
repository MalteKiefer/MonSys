-- 0005: host liveness state.
-- Tracks the latest derived status (online/stale/offline) per host so the
-- rules engine can react to transitions and the API can return a status
-- column without re-deriving on every request.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE host_status (
    host_id         UUID PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE,
    status          TEXT NOT NULL,                       -- online | stale | offline
    since           TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_check_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE host_status_history (
    id              BIGSERIAL PRIMARY KEY,
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    from_status     TEXT,
    to_status       TEXT NOT NULL,
    at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX host_status_history_host_at_idx ON host_status_history (host_id, at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS host_status_history CASCADE;
DROP TABLE IF EXISTS host_status CASCADE;
-- +goose StatementEnd
