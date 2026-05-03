-- AUDIT-069: Cardinality caps and string-length CHECKs for inventory tables.
--
-- Motivation:
--   A hostile or buggy agent could submit large numbers of unique device
--   names, workload external_ids, usernames, or package entries on every
--   bootstrap/heartbeat. Because these tables key on (host_id, ...) with
--   string columns of unbounded length, an adversary could bloat the
--   tables (and their indexes) until the database becomes unusable.
--
-- This migration adds two layers of defense:
--   1. CHECK constraints capping the length of string columns to sane,
--      generous upper bounds. Anything longer is almost certainly junk.
--   2. BEFORE INSERT triggers that abort INSERTs once a host has reached
--      a soft per-host row count cap on each inventory table.
--
-- Operator override path:
--   If a legitimate host genuinely exceeds one of these caps (e.g. a host
--   that really does manage 50k+ packages), the operator can run the
--   trigger drops manually, e.g.:
--
--       DROP TRIGGER trg_cap_packages ON packages;
--
--   This leaves the CHECK constraints in place but disables the row cap
--   for that table. Re-add the trigger when the situation is resolved.
--
-- The caps below are intentionally generous; they are intended as a last-
-- resort circuit breaker, not a primary quota mechanism.

-- +goose Up

-- +goose StatementBegin
ALTER TABLE disks
    ADD CONSTRAINT chk_disks_device_len     CHECK (char_length(device)     <= 256),
    ADD CONSTRAINT chk_disks_mountpoint_len CHECK (char_length(mountpoint) <= 1024);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE nics
    ADD CONSTRAINT chk_nics_name_len CHECK (char_length(name) <= 64);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE workloads
    ADD CONSTRAINT chk_workloads_external_id_len CHECK (char_length(external_id) <= 256),
    ADD CONSTRAINT chk_workloads_name_len        CHECK (name IS NULL OR char_length(name) <= 256);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE observed_users
    ADD CONSTRAINT chk_observed_users_username_len CHECK (char_length(username) <= 64);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE packages
    ADD CONSTRAINT chk_packages_name_len    CHECK (char_length(name)    <= 256),
    ADD CONSTRAINT chk_packages_version_len CHECK (char_length(version) <= 256);
-- +goose StatementEnd

-- Per-host row count cap trigger functions.
-- Each function enforces a soft cap on its own table; the cap value is
-- baked into the function so the SQL plan can be cached cleanly.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_disks_cap() RETURNS trigger AS $$
BEGIN
    IF (SELECT count(*) FROM disks WHERE host_id = NEW.host_id) >= 500 THEN
        RAISE EXCEPTION 'cardinality cap exceeded: host % already has >= 500 disks (AUDIT-069)', NEW.host_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_nics_cap() RETURNS trigger AS $$
BEGIN
    IF (SELECT count(*) FROM nics WHERE host_id = NEW.host_id) >= 100 THEN
        RAISE EXCEPTION 'cardinality cap exceeded: host % already has >= 100 nics (AUDIT-069)', NEW.host_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_workloads_cap() RETURNS trigger AS $$
BEGIN
    IF (SELECT count(*) FROM workloads WHERE host_id = NEW.host_id) >= 1000 THEN
        RAISE EXCEPTION 'cardinality cap exceeded: host % already has >= 1000 workloads (AUDIT-069)', NEW.host_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_observed_users_cap() RETURNS trigger AS $$
BEGIN
    IF (SELECT count(*) FROM observed_users WHERE host_id = NEW.host_id) >= 5000 THEN
        RAISE EXCEPTION 'cardinality cap exceeded: host % already has >= 5000 observed_users (AUDIT-069)', NEW.host_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_packages_cap() RETURNS trigger AS $$
BEGIN
    IF (SELECT count(*) FROM packages WHERE host_id = NEW.host_id) >= 50000 THEN
        RAISE EXCEPTION 'cardinality cap exceeded: host % already has >= 50000 packages (AUDIT-069)', NEW.host_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_cap_disks
    BEFORE INSERT ON disks
    FOR EACH ROW EXECUTE FUNCTION enforce_disks_cap();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_cap_nics
    BEFORE INSERT ON nics
    FOR EACH ROW EXECUTE FUNCTION enforce_nics_cap();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_cap_workloads
    BEFORE INSERT ON workloads
    FOR EACH ROW EXECUTE FUNCTION enforce_workloads_cap();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_cap_observed_users
    BEFORE INSERT ON observed_users
    FOR EACH ROW EXECUTE FUNCTION enforce_observed_users_cap();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_cap_packages
    BEFORE INSERT ON packages
    FOR EACH ROW EXECUTE FUNCTION enforce_packages_cap();
-- +goose StatementEnd


-- +goose Down

-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_cap_packages       ON packages;
DROP TRIGGER IF EXISTS trg_cap_observed_users ON observed_users;
DROP TRIGGER IF EXISTS trg_cap_workloads      ON workloads;
DROP TRIGGER IF EXISTS trg_cap_nics           ON nics;
DROP TRIGGER IF EXISTS trg_cap_disks          ON disks;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS enforce_packages_cap();
DROP FUNCTION IF EXISTS enforce_observed_users_cap();
DROP FUNCTION IF EXISTS enforce_workloads_cap();
DROP FUNCTION IF EXISTS enforce_nics_cap();
DROP FUNCTION IF EXISTS enforce_disks_cap();
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE packages
    DROP CONSTRAINT IF EXISTS chk_packages_version_len,
    DROP CONSTRAINT IF EXISTS chk_packages_name_len;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE observed_users
    DROP CONSTRAINT IF EXISTS chk_observed_users_username_len;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE workloads
    DROP CONSTRAINT IF EXISTS chk_workloads_name_len,
    DROP CONSTRAINT IF EXISTS chk_workloads_external_id_len;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE nics
    DROP CONSTRAINT IF EXISTS chk_nics_name_len;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE disks
    DROP CONSTRAINT IF EXISTS chk_disks_mountpoint_len,
    DROP CONSTRAINT IF EXISTS chk_disks_device_len;
-- +goose StatementEnd
