-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS timescaledb;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE hosts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    hostname        TEXT NOT NULL,
    machine_id      TEXT UNIQUE,
    os              TEXT,
    kernel          TEXT,
    arch            TEXT,
    distro          TEXT,
    cpu_model       TEXT,
    cpu_cores       INT,
    ram_total_bytes BIGINT,
    agent_version   TEXT,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    labels          JSONB NOT NULL DEFAULT '{}'::jsonb,
    revoked_at      TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE disks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    device          TEXT NOT NULL,
    mountpoint      TEXT NOT NULL,
    fstype          TEXT,
    size_bytes      BIGINT,
    is_removable    BOOLEAN DEFAULT FALSE,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (host_id, device, mountpoint)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE nics (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    mac             TEXT,
    speed_mbps      INT,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (host_id, name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE workloads (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    name            TEXT,
    image           TEXT,
    state           TEXT,
    labels          JSONB NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (host_id, kind, external_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE packages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    manager         TEXT NOT NULL,
    name            TEXT NOT NULL,
    version         TEXT NOT NULL,
    arch            TEXT,
    source_repo     TEXT,
    installed_at    TIMESTAMPTZ,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (host_id, manager, name, arch)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE package_updates (
    host_id              UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    manager              TEXT NOT NULL,
    name                 TEXT NOT NULL,
    arch                 TEXT NOT NULL DEFAULT '',
    current_version      TEXT NOT NULL,
    available_version    TEXT NOT NULL,
    source_repo          TEXT,
    is_security          BOOLEAN NOT NULL DEFAULT FALSE,
    detected_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (host_id, manager, name, arch)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE package_repo_state (
    host_id              UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    manager              TEXT NOT NULL,
    metadata_mtime       TIMESTAMPTZ,
    metadata_age_seconds BIGINT,
    refreshed_externally BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (host_id, manager)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE agent_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash      BYTEA UNIQUE NOT NULL,
    description     TEXT,
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ,
    used_by_host    UUID REFERENCES hosts(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE agent_keys (
    host_id         UUID PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE,
    key_hash        BYTEA NOT NULL,
    rotated_at      TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT UNIQUE NOT NULL,
    password_hash   TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'user',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    disabled_at     TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor           TEXT,
    action          TEXT NOT NULL,
    target          TEXT,
    detail          JSONB NOT NULL DEFAULT '{}'::jsonb
);
-- +goose StatementEnd

-- Time-series tables (Hypertables)

-- +goose StatementBegin
CREATE TABLE metrics_system (
    time            TIMESTAMPTZ NOT NULL,
    host_id         UUID NOT NULL,
    cpu_usage_pct   DOUBLE PRECISION,
    cpu_per_core    DOUBLE PRECISION[],
    load_1          DOUBLE PRECISION,
    load_5          DOUBLE PRECISION,
    load_15         DOUBLE PRECISION,
    ram_used_bytes  BIGINT,
    ram_avail_bytes BIGINT,
    swap_used_bytes BIGINT,
    uptime_sec      BIGINT
);
SELECT create_hypertable('metrics_system', 'time', chunk_time_interval => INTERVAL '1 day');
CREATE INDEX ON metrics_system (host_id, time DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE metrics_disk (
    time            TIMESTAMPTZ NOT NULL,
    host_id         UUID NOT NULL,
    disk_id         UUID NOT NULL,
    used_bytes      BIGINT,
    free_bytes      BIGINT,
    inodes_used     BIGINT,
    inodes_free     BIGINT,
    read_bytes      BIGINT,
    write_bytes     BIGINT,
    read_ops        BIGINT,
    write_ops       BIGINT,
    io_time_ms      BIGINT
);
SELECT create_hypertable('metrics_disk', 'time', chunk_time_interval => INTERVAL '1 day');
CREATE INDEX ON metrics_disk (host_id, disk_id, time DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE metrics_net (
    time            TIMESTAMPTZ NOT NULL,
    host_id         UUID NOT NULL,
    nic_id          UUID NOT NULL,
    rx_bytes        BIGINT,
    tx_bytes        BIGINT,
    rx_pkts         BIGINT,
    tx_pkts         BIGINT,
    rx_errs         BIGINT,
    tx_errs         BIGINT,
    rx_drops        BIGINT,
    tx_drops        BIGINT
);
SELECT create_hypertable('metrics_net', 'time', chunk_time_interval => INTERVAL '1 day');
CREATE INDEX ON metrics_net (host_id, nic_id, time DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE metrics_workload (
    time              TIMESTAMPTZ NOT NULL,
    host_id           UUID NOT NULL,
    workload_id       UUID NOT NULL,
    cpu_usage_pct     DOUBLE PRECISION,
    mem_used_bytes    BIGINT,
    mem_limit_bytes   BIGINT,
    net_rx_bytes      BIGINT,
    net_tx_bytes      BIGINT,
    block_read_bytes  BIGINT,
    block_write_bytes BIGINT,
    state             TEXT
);
SELECT create_hypertable('metrics_workload', 'time', chunk_time_interval => INTERVAL '1 day');
CREATE INDEX ON metrics_workload (host_id, workload_id, time DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE metrics_packages_summary (
    time              TIMESTAMPTZ NOT NULL,
    host_id           UUID NOT NULL,
    installed_count   INT,
    updates_count     INT,
    security_updates  INT,
    metadata_age_sec  BIGINT
);
SELECT create_hypertable('metrics_packages_summary', 'time', chunk_time_interval => INTERVAL '7 days');
CREATE INDEX ON metrics_packages_summary (host_id, time DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS metrics_packages_summary CASCADE;
DROP TABLE IF EXISTS metrics_workload CASCADE;
DROP TABLE IF EXISTS metrics_net CASCADE;
DROP TABLE IF EXISTS metrics_disk CASCADE;
DROP TABLE IF EXISTS metrics_system CASCADE;
DROP TABLE IF EXISTS audit_log CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS agent_keys CASCADE;
DROP TABLE IF EXISTS agent_tokens CASCADE;
DROP TABLE IF EXISTS package_repo_state CASCADE;
DROP TABLE IF EXISTS package_updates CASCADE;
DROP TABLE IF EXISTS packages CASCADE;
DROP TABLE IF EXISTS workloads CASCADE;
DROP TABLE IF EXISTS nics CASCADE;
DROP TABLE IF EXISTS disks CASCADE;
DROP TABLE IF EXISTS hosts CASCADE;
-- +goose StatementEnd
