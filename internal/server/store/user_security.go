package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MalteKiefer/MonSys/internal/server/auth2fa"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

var (
	ErrTOTPNotPending     = errors.New("no pending TOTP setup; call /v1/auth/2fa/setup first")
	ErrTOTPCodeInvalid    = errors.New("totp code invalid")
	ErrActionTokenInvalid = errors.New("action token invalid or expired")
)

// --- TOTP -----------------------------------------------------------------

// StartTOTPSetup generates a new secret and stores it as pending (enabled_at
// NULL). The previous setup, if any, is overwritten — admins use ResetTOTP
// for the explicit reset path.
func (s *Store) StartTOTPSetup(ctx context.Context, u User) (apitypes.TOTPSetupResponse, error) {
	secret, otpURL, qrPNG, backups, err := auth2fa.Generate(u.Email)
	if err != nil {
		return apitypes.TOTPSetupResponse{}, err
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO user_totp (user_id, secret_b32, backup_codes)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			secret_b32   = EXCLUDED.secret_b32,
			backup_codes = EXCLUDED.backup_codes,
			enabled_at   = NULL,
			last_used_at = NULL`,
		u.ID, secret, backups)
	if err != nil {
		return apitypes.TOTPSetupResponse{}, fmt.Errorf("user_totp upsert: %w", err)
	}
	return apitypes.TOTPSetupResponse{
		SecretB32:   secret,
		OTPAuthURL:  otpURL,
		QRPNGBase64: qrPNG,
		BackupCodes: backups,
	}, nil
}

// VerifyTOTP validates a code against the user's pending or active secret.
// On the first successful verification, enabled_at is set to now() — turning
// the pending setup into an active second factor. Backup codes are accepted
// at any time (consumed on use).
func (s *Store) VerifyTOTP(ctx context.Context, userID uuid.UUID, code string) error {
	var (
		secret  string
		enabled *time.Time
		backups []string
	)
	err := s.Pool.QueryRow(ctx,
		`SELECT secret_b32, enabled_at, backup_codes FROM user_totp WHERE user_id = $1`, userID,
	).Scan(&secret, &enabled, &backups)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTOTPNotPending
	}
	if err != nil {
		return err
	}

	if auth2fa.Validate(secret, strings.TrimSpace(code)) {
		_, err = s.Pool.Exec(ctx, `
			UPDATE user_totp SET
				enabled_at   = COALESCE(enabled_at, now()),
				last_used_at = now()
			WHERE user_id = $1`, userID)
		return err
	}

	// Try backup codes (they work whether or not TOTP is enabled).
	if remaining, ok := auth2fa.MatchAndConsume(backups, code); ok {
		_, err = s.Pool.Exec(ctx, `
			UPDATE user_totp SET
				enabled_at   = COALESCE(enabled_at, now()),
				backup_codes = $2,
				last_used_at = now()
			WHERE user_id = $1`, userID, remaining)
		return err
	}
	return ErrTOTPCodeInvalid
}

// DisableTOTP removes the user's TOTP record. Returns nil even if no record
// existed — disabling something that's not enabled is a no-op.
func (s *Store) DisableTOTP(ctx context.Context, userID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM user_totp WHERE user_id = $1`, userID)
	return err
}

// --- Action tokens (invite / password reset / email change) ---------------

// CreateActionToken issues a one-time URL-safe token for a flow.
func (s *Store) CreateActionToken(ctx context.Context, userID uuid.UUID, kind string, ttl time.Duration, payload map[string]any, createdBy string) (plaintext string, err error) {
	plaintext, err = generateActionToken()
	if err != nil {
		return "", err
	}
	hash := hashSecret(plaintext)
	expires := time.Now().Add(ttl).UTC()
	pl, _ := json.Marshal(orEmptyAny(payload))
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO user_action_tokens (user_id, token_hash, type, payload, expires_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, hash, kind, pl, expires, nullableString(createdBy))
	if err != nil {
		return "", err
	}
	return plaintext, nil
}

// ConsumeActionToken validates + marks the token used in one transaction.
// Returns the token's user_id, type, and payload.
func (s *Store) ConsumeActionToken(ctx context.Context, plaintext, expectedKind string) (uuid.UUID, map[string]any, error) {
	hash := hashSecret(plaintext)
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, nil, err
	}
	defer tx.Rollback(ctx)

	var (
		userID  uuid.UUID
		kind    string
		payload []byte
	)
	err = tx.QueryRow(ctx, `
		SELECT user_id, type, payload FROM user_action_tokens
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()`,
		hash).Scan(&userID, &kind, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil, ErrActionTokenInvalid
	}
	if err != nil {
		return uuid.Nil, nil, err
	}
	if expectedKind != "" && kind != expectedKind {
		return uuid.Nil, nil, ErrActionTokenInvalid
	}
	_, err = tx.Exec(ctx,
		`UPDATE user_action_tokens SET used_at = now() WHERE token_hash = $1`, hash)
	if err != nil {
		return uuid.Nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, nil, err
	}
	out := map[string]any{}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &out)
	}
	return userID, out, nil
}

func generateActionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mon_act_" + base64.RawURLEncoding.EncodeToString(b), nil
}

// --- Settings (key/value) ------------------------------------------------

// GetSetting retrieves a JSON-encoded setting; missing rows return ("", nil).
func (s *Store) GetSetting(ctx context.Context, key string) ([]byte, error) {
	var raw []byte
	err := s.Pool.QueryRow(ctx,
		`SELECT value FROM settings WHERE key = $1`, key,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return raw, err
}

// SetSetting upserts a JSON value.
func (s *Store) SetSetting(ctx context.Context, key string, value []byte, updatedBy string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO settings (key, value, updated_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET
			value      = EXCLUDED.value,
			updated_at = now(),
			updated_by = EXCLUDED.updated_by`,
		key, value, nullableString(updatedBy))
	return err
}

// GetPasswordPolicy returns the policy from settings, falling back to a sane
// default if the row is missing or malformed.
func (s *Store) GetPasswordPolicy(ctx context.Context) (apitypes.PasswordPolicy, error) {
	def := apitypes.PasswordPolicy{
		MinLength:    12,
		RequireUpper: true,
		RequireLower: true,
		RequireDigit: true,
	}
	raw, err := s.GetSetting(ctx, "password_policy")
	if err != nil {
		return def, err
	}
	if len(raw) == 0 {
		return def, nil
	}
	var p apitypes.PasswordPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		// Malformed JSON in settings shouldn't lock the login flow out of
		// a usable policy — fall back to the conservative defaults set
		// above. The setting can still be repaired via the admin UI.
		return def, nil //nolint:nilerr // intentional fallback to defaults
	}
	if p.MinLength < 4 {
		p.MinLength = def.MinLength
	}
	return p, nil
}

// SetPasswordPolicy serializes and stores the policy.
func (s *Store) SetPasswordPolicy(ctx context.Context, p apitypes.PasswordPolicy, updatedBy string) error {
	if p.MinLength < 4 {
		return errors.New("min_length must be at least 4")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, "password_policy", raw, updatedBy)
}

// passwordCharClasses tallies which character classes (upper/lower/digit/
// symbol) appear at least once in pw. Symbol is the catch-all bucket for
// anything that isn't ASCII A-Z, a-z, or 0-9 — matching what the policy
// schema means by "RequireSymbol".
type passwordCharClasses struct {
	HasUpper, HasLower, HasDigit, HasSymbol bool
}

func scanPasswordClasses(pw string) passwordCharClasses {
	var c passwordCharClasses
	for _, r := range pw {
		switch {
		case r >= 'A' && r <= 'Z':
			c.HasUpper = true
		case r >= 'a' && r <= 'z':
			c.HasLower = true
		case r >= '0' && r <= '9':
			c.HasDigit = true
		default:
			c.HasSymbol = true
		}
	}
	return c
}

// CheckPassword evaluates pw against the active password policy. Returns nil
// if pw passes; otherwise a list of human-readable problems joined with
// commas.
func (s *Store) CheckPassword(ctx context.Context, pw string) error {
	p, err := s.GetPasswordPolicy(ctx)
	if err != nil {
		return err
	}
	classes := scanPasswordClasses(pw)
	var problems []string
	if len(pw) < p.MinLength {
		problems = append(problems, fmt.Sprintf("at least %d characters", p.MinLength))
	}
	if p.RequireUpper && !classes.HasUpper {
		problems = append(problems, "at least one uppercase letter")
	}
	if p.RequireLower && !classes.HasLower {
		problems = append(problems, "at least one lowercase letter")
	}
	if p.RequireDigit && !classes.HasDigit {
		problems = append(problems, "at least one digit")
	}
	if p.RequireSymbol && !classes.HasSymbol {
		problems = append(problems, "at least one symbol")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("password policy: requires %s", strings.Join(problems, ", "))
}
