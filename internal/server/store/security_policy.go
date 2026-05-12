// Package store: security_policy.go holds the global security-policy KV
// (force_mode / grace_days / session caps) and the helpers that enforce or
// apply it.
//
// The policy itself lives in the generic `settings` table as a JSON blob under
// the key "security_policy" — the same extension point used by
// password_policy. Defaults are returned when the row is missing or
// unparseable so the system stays usable on a fresh install.
//
// The session-revocation helpers (RevokeAllSessions / RevokeUserSessions)
// live alongside the policy because the admin UI that flips force_mode is
// the primary caller — when policy gets stricter the operator typically
// wants to force everyone to re-authenticate.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// ForceMode values stored in apitypes.SecurityPolicy.ForceMode.
const (
	ForceModeOff             = "off"
	ForceMode2FAAny          = "2fa_any"
	ForceModePasskeyRequired = "passkey_required"
)

// defaultSecurityPolicy is returned when no settings row exists or the stored
// JSON cannot be parsed. Deliberately conservative-but-permissive: nothing is
// forced, but session caps are set so a misconfigured deploy still rotates
// tokens within a day.
var defaultSecurityPolicy = apitypes.SecurityPolicy{
	ForceMode:          ForceModeOff,
	GraceDays:          7,
	MaxSessionHours:    12,
	IdleTimeoutMinutes: 0,
}

// isValidForceMode reports whether v is one of the three accepted force-mode
// strings.
func isValidForceMode(v string) bool {
	switch v {
	case ForceModeOff, ForceMode2FAAny, ForceModePasskeyRequired:
		return true
	default:
		return false
	}
}

// GetSecurityPolicy returns the active policy, falling back to
// defaultSecurityPolicy when the row is missing or malformed. The returned
// policy is range-clamped so callers can trust its fields even if someone
// hand-edited the JSON in the database.
func (s *Store) GetSecurityPolicy(ctx context.Context) (apitypes.SecurityPolicy, error) {
	raw, err := s.GetSetting(ctx, "security_policy")
	if err != nil {
		return defaultSecurityPolicy, err
	}
	if len(raw) == 0 {
		return defaultSecurityPolicy, nil
	}
	var p apitypes.SecurityPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		slog.Warn("security_policy unmarshal failed; using default",
			"err", err, "raw_len", len(raw))
		return defaultSecurityPolicy, nil
	}

	// Defensive clamps in case the row was hand-edited.
	if !isValidForceMode(p.ForceMode) {
		p.ForceMode = ForceModeOff
	}
	if p.GraceDays < 0 {
		p.GraceDays = 0
	} else if p.GraceDays > 365 {
		p.GraceDays = 365
	}
	if p.MaxSessionHours < 1 {
		p.MaxSessionHours = 12
	} else if p.MaxSessionHours > 8760 {
		// 1 year cap
		p.MaxSessionHours = 8760
	}
	if p.IdleTimeoutMinutes < 0 {
		p.IdleTimeoutMinutes = 0
	} else if p.IdleTimeoutMinutes > 10080 {
		// 1 week cap
		p.IdleTimeoutMinutes = 10080
	}
	return p, nil
}

// SetSecurityPolicy validates and persists p. When the force-mode transitions
// from "off" to a stricter mode, every existing user whose force_grace_until
// is NULL is given the same grace deadline (now + GraceDays). When the mode
// transitions back to "off", force_grace_until is cleared everywhere so the
// next time the admin tightens the policy a fresh grace window applies.
func (s *Store) SetSecurityPolicy(ctx context.Context, p apitypes.SecurityPolicy, updatedBy string) error {
	if !isValidForceMode(p.ForceMode) {
		return errors.New("invalid force_mode")
	}
	if p.GraceDays < 0 || p.GraceDays > 365 {
		return errors.New("grace_days out of range (0-365)")
	}
	if p.MaxSessionHours < 1 || p.MaxSessionHours > 8760 {
		return errors.New("max_session_hours out of range (1-8760)")
	}
	if p.IdleTimeoutMinutes < 0 || p.IdleTimeoutMinutes > 10080 {
		return errors.New("idle_timeout_minutes out of range (0-10080)")
	}

	prev, err := s.GetSecurityPolicy(ctx)
	if err != nil {
		// GetSecurityPolicy already returns a usable default on parse errors;
		// a real DB failure is fatal here because we'd risk skipping the
		// transition fan-out below.
		return err
	}

	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := s.SetSetting(ctx, "security_policy", raw, updatedBy); err != nil {
		return err
	}

	switch {
	case prev.ForceMode == ForceModeOff && p.ForceMode != ForceModeOff:
		// Tightening: stamp a grace deadline on every user that doesn't
		// already have one. Existing deadlines are left in place so a re-
		// toggle doesn't grant a fresh extension.
		until := time.Now().Add(time.Duration(p.GraceDays) * 24 * time.Hour)
		if _, err := s.Pool.Exec(ctx,
			`UPDATE users SET force_grace_until = $1 WHERE force_grace_until IS NULL`,
			until,
		); err != nil {
			return err
		}
	case prev.ForceMode != ForceModeOff && p.ForceMode == ForceModeOff:
		// Releasing: clear every pending grace deadline so the next
		// transition into a strict mode computes from scratch.
		if _, err := s.Pool.Exec(ctx,
			`UPDATE users SET force_grace_until = NULL WHERE force_grace_until IS NOT NULL`,
		); err != nil {
			return err
		}
	}

	return nil
}

// RevokeAllSessions marks every active user session as revoked. When
// exceptToken is non-empty its sha256 hash is preserved (typically the admin's
// current session so they don't kick themselves out). Passing "" revokes
// every session in the table, including the caller's own.
//
// Returns the number of rows updated.
func (s *Store) RevokeAllSessions(ctx context.Context, exceptToken string) (int, error) {
	var exceptHash []byte
	if exceptToken != "" {
		exceptHash = hashSecret(exceptToken)
	} else {
		// Empty byte slice acts as the "no exception" sentinel — see SQL.
		exceptHash = []byte{}
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE user_sessions
		   SET revoked_at = now()
		 WHERE revoked_at IS NULL
		   AND ($1::bytea = ''::bytea OR token_hash <> $1)`,
		exceptHash,
	)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// RevokeUserSessions marks every active session for a single user as revoked.
// Used by admin actions like password reset, disable, or force_grace expiry.
func (s *Store) RevokeUserSessions(ctx context.Context, userID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE user_sessions
		   SET revoked_at = now()
		 WHERE user_id = $1
		   AND revoked_at IS NULL`,
		userID,
	)
	return err
}

// RevokeUserSessionsExcept revokes every active session for userID except the
// one whose plaintext token is exceptToken. Used by self-service credential
// rotations (e.g. ChangePassword) so the caller's current session survives
// while every other device is forced to re-auth. When exceptToken is empty,
// every session is revoked — matching RevokeUserSessions.
//
// audit 2026-05-12 F-3: introduced to support keeping the current session
// alive on a password change while booting every other device.
func (s *Store) RevokeUserSessionsExcept(ctx context.Context, userID uuid.UUID, exceptToken string) error {
	var exceptHash []byte
	if exceptToken != "" {
		exceptHash = hashSecret(exceptToken)
	} else {
		exceptHash = []byte{}
	}
	_, err := s.Pool.Exec(ctx, `
		UPDATE user_sessions
		   SET revoked_at = now()
		 WHERE user_id = $1
		   AND revoked_at IS NULL
		   AND ($2::bytea = ''::bytea OR token_hash <> $2)`,
		userID, exceptHash,
	)
	return err
}

// UserCompliesWithPolicy reports whether userID has the auth methods required
// by the active force-mode. When ForceMode == off, every user trivially
// complies and graceUntil is nil.
//
// When the user complies, any lingering force_grace_until row is cleared as a
// best-effort side effect — failures there are swallowed because compliance
// is the authoritative answer.
//
// The caller is responsible for deciding what to do with a non-complying user
// who still has time on the clock (graceUntil != nil && time.Now().Before
// (*graceUntil)). Typical policy: allow read-only endpoints, block writes.
func (s *Store) UserCompliesWithPolicy(ctx context.Context, userID uuid.UUID) (complies bool, graceUntil *time.Time, err error) {
	pol, err := s.GetSecurityPolicy(ctx)
	if err != nil {
		return false, nil, err
	}
	if pol.ForceMode == ForceModeOff {
		return true, nil, nil
	}

	var (
		totpActive   bool
		passkeyCount int
		grace        *time.Time
	)
	err = s.Pool.QueryRow(ctx, `
		SELECT
		  COALESCE((SELECT enabled_at IS NOT NULL FROM user_totp WHERE user_id = $1), false) AS totp_active,
		  (SELECT count(*) FROM user_credentials WHERE user_id = $1)                          AS passkey_count,
		  (SELECT force_grace_until FROM users WHERE id = $1)                                 AS grace`,
		userID,
	).Scan(&totpActive, &passkeyCount, &grace)
	if err != nil {
		return false, nil, err
	}

	switch pol.ForceMode {
	case ForceMode2FAAny:
		complies = totpActive || passkeyCount > 0
	case ForceModePasskeyRequired:
		complies = passkeyCount > 0
	}

	if complies && grace != nil {
		// Best-effort clear; the answer is already correct regardless.
		_, _ = s.Pool.Exec(ctx,
			`UPDATE users SET force_grace_until = NULL WHERE id = $1 AND force_grace_until IS NOT NULL`,
			userID,
		)
		grace = nil
	}

	return complies, grace, nil
}
