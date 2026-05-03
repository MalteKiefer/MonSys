-- 0006: active monitors (server-side periodic probes).
-- Each row defines one probe: HTTPS cert expiry, DB reachability, generic
-- HTTP, generic TCP. Results land in a hypertable so we can graph latency
-- and uptime over time.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE monitors (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type            TEXT NOT NULL,                       -- cert | postgres | mysql | mongodb | http | tcp
    name            TEXT NOT NULL,
    target          TEXT NOT NULL,                       -- hostname:port for tcp/cert; URL for http; DSN for db
    params          JSONB NOT NULL DEFAULT '{}'::jsonb,  -- per-type knobs (timeout, expected_status, …)
    interval_sec    INT NOT NULL DEFAULT 60,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      TEXT,
    last_check_at   TIMESTAMPTZ,
    last_status     TEXT,                                -- ok | warn | fail | unknown
    last_latency_ms INT,
    last_detail     TEXT,
    UNIQUE (type, name)
);
CREATE INDEX monitors_enabled_idx ON monitors (enabled, type);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE monitor_results (
    time            TIMESTAMPTZ NOT NULL,
    monitor_id      UUID NOT NULL,
    status          TEXT NOT NULL,
    latency_ms      INT,
    detail          TEXT
);
SELECT create_hypertable('monitor_results', 'time', chunk_time_interval => INTERVAL '7 days');
CREATE INDEX ON monitor_results (monitor_id, time DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS monitor_results CASCADE;
DROP TABLE IF EXISTS monitors CASCADE;
-- +goose StatementEnd
