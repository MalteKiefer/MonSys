-- 0027: Docker image update detection.
--
-- The agent's docker collector queries each running container's source
-- registry for the latest manifest digest of its image:tag, compares that
-- against the runtime image digest, and reports the verdict in the
-- workloads ingest path.
--
-- Columns:
--   * current_digest    : sha256:… as reported by `docker inspect`
--   * latest_digest     : sha256:… returned by the upstream registry's
--                         Docker-Content-Digest header
--   * update_available  : agent-computed (current_digest != latest_digest
--                         AND both populated). Persisted as-is so the UI
--                         can render badges without re-running the diff.
--   * update_checked_at : wall-clock of the last time the server accepted
--                         an update-availability report for this row
--
-- The partial index supports the per-host "N containers have updates"
-- count surfaced on the Hosts list. We only index TRUE rows because those
-- are the ones the UI ever wants to count or filter on.
--
-- This migration is idempotent: re-running it is a no-op (every clause
-- guards with IF NOT EXISTS).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE workloads
    ADD COLUMN IF NOT EXISTS current_digest    TEXT,
    ADD COLUMN IF NOT EXISTS latest_digest     TEXT,
    ADD COLUMN IF NOT EXISTS update_available  BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS update_checked_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS workloads_update_available_idx
    ON workloads (host_id)
    WHERE update_available = TRUE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS workloads_update_available_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE workloads
    DROP COLUMN IF EXISTS update_checked_at,
    DROP COLUMN IF EXISTS update_available,
    DROP COLUMN IF EXISTS latest_digest,
    DROP COLUMN IF EXISTS current_digest;
-- +goose StatementEnd
