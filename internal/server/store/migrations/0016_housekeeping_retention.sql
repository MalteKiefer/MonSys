-- 0016: housekeeping retention for previously unbounded tables.
--
-- alert_history (added in 0007) was a plain Postgres table with no retention.
-- Convert it to a Timescale hypertable on `at` and add a 365 day retention
-- policy. host_status_history (added in 0005) is also unbounded; convert and
-- apply a 90 day policy.
--
-- DSGVO/GDPR rationale (mirrors 0012_retention.sql):
--   * alert_history (365 Tage):
--     Notification-Audit-Spur. Ein Jahr deckt typische Compliance- und
--     Forensik-Auswertungen ab; darüber hinaus besteht kein berechtigtes
--     Interesse an der Aufbewahrung der enthaltenen Empfänger-/Subject-Daten.
--
--   * host_status_history (90 Tage):
--     Online/Offline-Übergänge je Host. Drei Monate decken übliche SLA- und
--     Incident-Reviews ab; ältere Übergänge sind operativ wertlos.
--
-- Timescale verlangt, dass die Partitionierungsspalte Teil eines eventuellen
-- UNIQUE/PRIMARY KEY ist. Beide Tabellen haben aktuell `id BIGSERIAL PRIMARY
-- KEY`; der PK wird auf (id, at) erweitert, damit `create_hypertable` ohne
-- migrate_data-Konflikte funktioniert.

-- +goose Up

-- alert_history: erweitere PK um die Zeitspalte, dann Hypertable + Policy.
-- +goose StatementBegin
ALTER TABLE alert_history DROP CONSTRAINT IF EXISTS alert_history_pkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE alert_history ADD PRIMARY KEY (id, at);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT create_hypertable('alert_history', 'at', if_not_exists => TRUE, migrate_data => TRUE);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT add_retention_policy('alert_history', INTERVAL '365 days', if_not_exists => TRUE);
-- +goose StatementEnd

-- host_status_history: gleiches Verfahren, kürzere Aufbewahrung.
-- +goose StatementBegin
ALTER TABLE host_status_history DROP CONSTRAINT IF EXISTS host_status_history_pkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE host_status_history ADD PRIMARY KEY (id, at);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT create_hypertable('host_status_history', 'at', if_not_exists => TRUE, migrate_data => TRUE);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT add_retention_policy('host_status_history', INTERVAL '90 days', if_not_exists => TRUE);
-- +goose StatementEnd

-- +goose Down
-- Nur die Retention-Policies werden rückgängig gemacht. Die Hypertable-
-- Konvertierung wird bewusst nicht zurückgenommen — das Zurückwandeln in eine
-- Plain-Table ist destruktiv und für unsere Zwecke nicht erforderlich.
-- +goose StatementBegin
SELECT remove_retention_policy('host_status_history', if_exists => TRUE);
-- +goose StatementEnd

-- +goose StatementBegin
SELECT remove_retention_policy('alert_history', if_exists => TRUE);
-- +goose StatementEnd
