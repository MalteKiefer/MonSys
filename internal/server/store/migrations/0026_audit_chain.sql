-- 0026: tamper-evident audit_log (AUDIT-701, CWE-1232).
--
-- Each audit_log row carries a SHA-256 hash chain. The hash for row N is:
--
--   hash_N = sha256( prev_hash_{N-1} || actor || action || target ||
--                    detail_text || at_text )
--
-- Where:
--   * prev_hash_{N-1} is the hash of the row with the largest at < at_N
--     (lexicographically the previous row when ordered by at). For the very
--     first row (or rows that tie on at) we substitute an all-zeroes 32-byte
--     seed so the chain has a deterministic root.
--   * actor / target are coalesced to '' when NULL.
--   * detail_text is `coalesce(detail::text, '')` — JSONB rendered as text.
--   * at_text is `at::text` — the canonical Postgres timestamptz rendering.
--
-- The trigger fills NEW.prev_hash and NEW.hash on every INSERT, so existing
-- callers (s.AuditLog) keep working unchanged. A one-shot backfill walks
-- pre-existing rows in `at` order to seed the chain, all wrapped in the
-- migration transaction so partial state can never persist.
--
-- Operators verify the chain via `mon-server --verify-audit-chain` (see
-- internal/server/store/audit_chain.go).

-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pgcrypto;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS prev_hash BYTEA,
    ADD COLUMN IF NOT EXISTS hash      BYTEA;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_log_hash_trigger() RETURNS trigger AS $$
DECLARE
    v_prev BYTEA;
BEGIN
    -- Use the hash of the row with the largest `at` strictly less than the
    -- new row's `at`. Ties are broken by id (BIGSERIAL is monotonic). If no
    -- such row exists, seed with 32 zero bytes.
    SELECT a.hash
      INTO v_prev
      FROM audit_log a
     WHERE a.at < NEW.at
        OR (a.at = NEW.at AND a.id < COALESCE(NEW.id, 0))
     ORDER BY a.at DESC, a.id DESC
     LIMIT 1;

    IF v_prev IS NULL THEN
        v_prev := decode('0000000000000000000000000000000000000000000000000000000000000000', 'hex');
    END IF;

    NEW.prev_hash := v_prev;
    NEW.hash := digest(
        v_prev
        || convert_to(COALESCE(NEW.actor,  ''), 'UTF8')
        || convert_to(COALESCE(NEW.action, ''), 'UTF8')
        || convert_to(COALESCE(NEW.target, ''), 'UTF8')
        || convert_to(COALESCE(NEW.detail::text, ''), 'UTF8')
        || convert_to(NEW.at::text, 'UTF8'),
        'sha256'
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS audit_log_hash_chain ON audit_log;
CREATE TRIGGER audit_log_hash_chain
    BEFORE INSERT ON audit_log
    FOR EACH ROW
    EXECUTE FUNCTION audit_log_hash_trigger();
-- +goose StatementEnd

-- Backfill existing rows. The trigger already does the right thing for new
-- rows, but pre-existing rows have NULL hash columns; walk them in `at` order
-- and compute the chain inline. This runs inside the migration transaction,
-- so failure rolls everything back.
-- +goose StatementBegin
DO $$
DECLARE
    r        RECORD;
    v_prev   BYTEA := decode('0000000000000000000000000000000000000000000000000000000000000000', 'hex');
    v_hash   BYTEA;
BEGIN
    FOR r IN
        SELECT id, at, actor, action, target, detail
          FROM audit_log
         WHERE hash IS NULL
         ORDER BY at ASC, id ASC
    LOOP
        v_hash := digest(
            v_prev
            || convert_to(COALESCE(r.actor,  ''), 'UTF8')
            || convert_to(COALESCE(r.action, ''), 'UTF8')
            || convert_to(COALESCE(r.target, ''), 'UTF8')
            || convert_to(COALESCE(r.detail::text, ''), 'UTF8')
            || convert_to(r.at::text, 'UTF8'),
            'sha256'
        );
        UPDATE audit_log
           SET prev_hash = v_prev,
               hash      = v_hash
         WHERE id = r.id;
        v_prev := v_hash;
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS audit_log_hash_chain ON audit_log;
DROP FUNCTION IF EXISTS audit_log_hash_trigger();
ALTER TABLE audit_log
    DROP COLUMN IF EXISTS hash,
    DROP COLUMN IF EXISTS prev_hash;
-- +goose StatementEnd
