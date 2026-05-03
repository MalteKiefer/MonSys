-- 0002: virt inventory, observed users, login events, firewall, fail2ban, crowdsec.
-- All tables are host-scoped and CASCADE on host delete.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE vms (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,                       -- kvm | lxc | libvirt-lxc | …
    external_id     TEXT NOT NULL,                       -- uuid for libvirt, name for lxc
    name            TEXT,
    state           TEXT,                                -- running, paused, stopped
    vcpu            INT,
    mem_bytes       BIGINT,
    autostart       BOOLEAN,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (host_id, kind, external_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE observed_users (
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    username        TEXT NOT NULL,
    uid             INT,
    gid             INT,
    shell           TEXT,
    home            TEXT,
    is_sudoer       BOOLEAN NOT NULL DEFAULT FALSE,
    is_system       BOOLEAN NOT NULL DEFAULT FALSE,      -- uid < 1000 (Linux convention)
    last_login_at   TIMESTAMPTZ,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (host_id, username)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE login_events (
    time            TIMESTAMPTZ NOT NULL,
    host_id         UUID NOT NULL,
    username        TEXT,
    source_ip       TEXT,
    method          TEXT,                                -- ssh, login, su, sudo, …
    success         BOOLEAN NOT NULL,
    detail          TEXT
);
SELECT create_hypertable('login_events', 'time', chunk_time_interval => INTERVAL '7 days');
CREATE INDEX ON login_events (host_id, time DESC);
CREATE INDEX ON login_events (host_id, success, time DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE firewall_status (
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    engine          TEXT NOT NULL,                       -- ufw, nftables, iptables
    active          BOOLEAN NOT NULL DEFAULT FALSE,
    default_input   TEXT,
    default_output  TEXT,
    default_forward TEXT,
    rule_count      INT,
    snapshot_excerpt TEXT,                               -- first ~4 KiB of dump for context
    snapshot_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (host_id, engine)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE fail2ban_jails (
    host_id           UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    jail              TEXT NOT NULL,
    currently_failed  INT,
    total_failed      INT,
    currently_banned  INT,
    total_banned      INT,
    banned_ips        TEXT[],
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (host_id, jail)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE crowdsec_decisions (
    host_id         UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    decision_id     TEXT NOT NULL,
    origin          TEXT,
    scope           TEXT,                                -- Ip, Range, Country, …
    target          TEXT,
    decision_type   TEXT,                                -- ban, captcha, …
    reason          TEXT,
    until           TIMESTAMPTZ,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (host_id, decision_id)
);
CREATE INDEX ON crowdsec_decisions (host_id, until DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS crowdsec_decisions CASCADE;
DROP TABLE IF EXISTS fail2ban_jails CASCADE;
DROP TABLE IF EXISTS firewall_status CASCADE;
DROP TABLE IF EXISTS login_events CASCADE;
DROP TABLE IF EXISTS observed_users CASCADE;
DROP TABLE IF EXISTS vms CASCADE;
-- +goose StatementEnd
