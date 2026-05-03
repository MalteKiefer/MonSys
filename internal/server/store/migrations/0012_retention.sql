-- TimescaleDB retention policies for hypertables.
--
-- Rationale (DSGVO/GDPR-Konformität):
-- Personenbezogene und betriebliche Telemetrie darf nur so lange gespeichert
-- werden, wie es für den ursprünglichen Erhebungszweck erforderlich ist
-- (Art. 5 Abs. 1 lit. e DSGVO - Speicherbegrenzung / "Storage Limitation").
-- Daher wird für jede Hypertable eine Retention-Policy definiert, die ältere
-- Chunks automatisch verwirft:
--
--   * metrics_system / metrics_disk / metrics_net / metrics_workload (30 Tage):
--     Hochfrequente Host-Telemetrie - 30 Tage decken übliche Trend-/Incident-
--     Analysen ab; darüber hinaus besteht kein berechtigtes Interesse.
--
--   * metrics_packages_summary (90 Tage):
--     Aggregierte Paket-/Update-Stände dienen Compliance-Reports und werden
--     länger aufbewahrt, enthalten aber keine personenbezogenen Daten.
--
--   * monitor_results (60 Tage):
--     Ergebnisse synthetischer Monitore - SLA-Auswertungen über zwei Monate.
--
--   * login_events (180 Tage):
--     Authentifizierungs-/Anmeldeereignisse sind sicherheitsrelevant und
--     dienen u.a. der Nachvollziehbarkeit unautorisierter Zugriffsversuche
--     (Art. 32 DSGVO - Sicherheit der Verarbeitung). 180 Tage entsprechen
--     dem üblichen Forensik-Fenster und der Empfehlung des BSI / IT-Grund-
--     schutz für Anmelde-Logs; eine längere Speicherung wäre ohne konkreten
--     Anlass nicht mit dem Grundsatz der Datenminimierung vereinbar.

-- +goose Up
-- +goose StatementBegin
SELECT add_retention_policy('metrics_system', INTERVAL '30 days');
-- +goose StatementEnd

-- +goose StatementBegin
SELECT add_retention_policy('metrics_disk', INTERVAL '30 days');
-- +goose StatementEnd

-- +goose StatementBegin
SELECT add_retention_policy('metrics_net', INTERVAL '30 days');
-- +goose StatementEnd

-- +goose StatementBegin
SELECT add_retention_policy('metrics_workload', INTERVAL '30 days');
-- +goose StatementEnd

-- +goose StatementBegin
SELECT add_retention_policy('metrics_packages_summary', INTERVAL '90 days');
-- +goose StatementEnd

-- +goose StatementBegin
SELECT add_retention_policy('monitor_results', INTERVAL '60 days');
-- +goose StatementEnd

-- +goose StatementBegin
SELECT add_retention_policy('login_events', INTERVAL '180 days');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT remove_retention_policy('login_events');
-- +goose StatementEnd

-- +goose StatementBegin
SELECT remove_retention_policy('monitor_results');
-- +goose StatementEnd

-- +goose StatementBegin
SELECT remove_retention_policy('metrics_packages_summary');
-- +goose StatementEnd

-- +goose StatementBegin
SELECT remove_retention_policy('metrics_workload');
-- +goose StatementEnd

-- +goose StatementBegin
SELECT remove_retention_policy('metrics_net');
-- +goose StatementEnd

-- +goose StatementBegin
SELECT remove_retention_policy('metrics_disk');
-- +goose StatementEnd

-- +goose StatementBegin
SELECT remove_retention_policy('metrics_system');
-- +goose StatementEnd
