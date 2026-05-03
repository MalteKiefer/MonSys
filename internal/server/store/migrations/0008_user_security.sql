-- 0008: per-user TOTP, email change verification, invite + password-reset
-- tokens, and a settings table for password policy and other admin knobs.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE user_totp (
    user_id          UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_b32       TEXT NOT NULL,                       -- base32 TOTP secret (sensitive)
    enabled_at       TIMESTAMPTZ,                         -- NULL until first verified code
    backup_codes     TEXT[] NOT NULL DEFAULT '{}',        -- one-time codes; consumed by removal from array
    last_used_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- One-shot tokens for email-verification-on-change, password reset (admin or
-- self), and user invites. type indicates which flow consumed the token.
CREATE TABLE user_action_tokens (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash    BYTEA NOT NULL UNIQUE,
    type          TEXT NOT NULL,                          -- email_change | password_reset | invite
    payload       JSONB NOT NULL DEFAULT '{}'::jsonb,     -- type-specific (e.g. new_email)
    expires_at    TIMESTAMPTZ NOT NULL,
    used_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by    TEXT
);
CREATE INDEX user_action_tokens_user_idx ON user_action_tokens (user_id);
CREATE INDEX user_action_tokens_type_idx ON user_action_tokens (type);
-- +goose StatementEnd

-- +goose StatementBegin
-- Generic key/value settings. Used for password_policy, signup, and any
-- future admin-tweakable knob that doesn't deserve its own table.
CREATE TABLE settings (
    key            TEXT PRIMARY KEY,
    value          JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by     TEXT
);
INSERT INTO settings (key, value) VALUES
  ('password_policy', '{"min_length": 12, "require_upper": true, "require_lower": true, "require_digit": true, "require_symbol": false, "max_age_days": 0}'::jsonb);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS settings CASCADE;
DROP TABLE IF EXISTS user_action_tokens CASCADE;
DROP TABLE IF EXISTS user_totp CASCADE;
-- +goose StatementEnd
