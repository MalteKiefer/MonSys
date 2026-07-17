-- 0035: mail-stack monitoring tables.
--
-- host_mail_status: latest MTA/rspamd/queue report per host (plain table,
-- one row per host, replaced on every agent push).
--
-- metrics_mail: time-series queue + rspamd counters, stored as a TimescaleDB
-- hypertable on `time` with a 90-day retention policy (same window used by
-- the other metrics_* tables since 0033).

-- +goose Up

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS host_mail_status (
    host_id    UUID PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    report     JSONB       NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS metrics_mail (
    time              TIMESTAMPTZ NOT NULL,
    host_id           UUID        NOT NULL,
    queue_active      INT         NOT NULL DEFAULT 0,
    queue_deferred    INT         NOT NULL DEFAULT 0,
    queue_hold        INT         NOT NULL DEFAULT 0,
    queue_total       INT         NOT NULL DEFAULT 0,
    rspamd_greylisted BIGINT      NOT NULL DEFAULT 0,
    rspamd_rejected   BIGINT      NOT NULL DEFAULT 0
);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT create_hypertable('metrics_mail', 'time', if_not_exists => TRUE);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT add_retention_policy('metrics_mail', INTERVAL '90 days', if_not_exists => TRUE);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS metrics_mail_host_time_idx ON metrics_mail (host_id, time DESC);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
SELECT remove_retention_policy('metrics_mail', if_exists => TRUE);
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS metrics_mail;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS host_mail_status;
-- +goose StatementEnd
