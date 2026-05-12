-- 0029: passkey / WebAuthn support.
-- Adds the user_credentials table holding one row per registered authenticator
-- (raw credential_id bytes, COSE public key, sign counter, transports, backup
-- flags) plus two columns on users: webauthn_handle (stable opaque 16-byte
-- handle used as user.id at the WebAuthn layer, so email/UUID never leak to
-- authenticators) and force_grace_until (deadline by which a user must enroll
-- a 2FA method when force_mode is active; NULL = no grace pending).

-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS user_credentials (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id    BYTEA NOT NULL UNIQUE,
    public_key       BYTEA NOT NULL,
    aaguid           UUID,
    sign_count       BIGINT NOT NULL DEFAULT 0,
    transports       TEXT[] NOT NULL DEFAULT '{}'::text[],
    backup_eligible  BOOLEAN NOT NULL DEFAULT false,
    backup_state     BOOLEAN NOT NULL DEFAULT false,
    attestation_type TEXT NOT NULL DEFAULT 'none',
    name             TEXT NOT NULL DEFAULT 'Passkey',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at     TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_user_credentials_user ON user_credentials(user_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users ADD COLUMN IF NOT EXISTS webauthn_handle BYTEA UNIQUE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users ADD COLUMN IF NOT EXISTS force_grace_until TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS force_grace_until;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS webauthn_handle;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_user_credentials_user;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS user_credentials;
-- +goose StatementEnd
