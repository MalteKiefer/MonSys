// Package store: user_email_change.go implements the verified email-change
// flow. The flow is:
//
//  1. Authenticated user posts the new address. We mint a one-hour
//     CreateActionToken of kind "email_change" whose JSON payload carries
//     {"new_email": "..."} and ship the token URL to the NEW address — proving
//     the new address is under their control before we rotate it.
//  2. The link lands on the SPA which POSTs the token (no auth header — the
//     user may already be logged out by then) to /v1/auth/email/confirm. We
//     ConsumeActionToken, rewrite users.email, and revoke every session for
//     that user so they must log in fresh on every device.
//
// CLI recovery (admin shell) has its own SetEmailUnconditional path which
// skips the round-trip entirely.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RequestEmailChange creates a confirmation token tying userID to newEmail.
// The token kind is "email_change"; payload carries {"new_email":"…"}. TTL
// 1 hour. The plaintext token is returned so the caller can ship it via
// invite mail to the NEW address (not the current one — the new address
// must prove control).
func (s *Store) RequestEmailChange(ctx context.Context, userID uuid.UUID, newEmail, createdBy string) (string, error) {
	if newEmail == "" {
		return "", errors.New("new_email required")
	}
	// Refuse if the new address is already in use by another row.
	var existingID uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1)`, newEmail).Scan(&existingID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if err == nil && existingID != userID {
		return "", ErrUserExists
	}
	return s.CreateActionToken(ctx, userID, "email_change", time.Hour, map[string]any{"new_email": newEmail}, createdBy)
}

// ConsumeEmailChange validates the token + updates users.email + revokes
// every session for that user so they have to log in fresh on every device.
// Returns the user record post-update.
func (s *Store) ConsumeEmailChange(ctx context.Context, plaintext string) (User, error) {
	uid, payload, err := s.ConsumeActionToken(ctx, plaintext, "email_change")
	if err != nil {
		return User{}, err
	}
	newEmail, _ := payload["new_email"].(string)
	if newEmail == "" {
		return User{}, errors.New("token missing new_email")
	}
	// Same uniqueness check as the request path, in case someone else
	// grabbed the address in the meantime.
	var existingID uuid.UUID
	err = s.Pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1)`, newEmail).Scan(&existingID)
	if err == nil && existingID != uid {
		return User{}, ErrUserExists
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return User{}, err
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE users SET email = $2 WHERE id = $1`, uid, newEmail); err != nil {
		if pgIsUniqueViolation(err) {
			return User{}, ErrUserExists
		}
		return User{}, err
	}
	// Force re-login everywhere — the security argument is that an old
	// session whose owning email just changed should not stay alive.
	_ = s.RevokeUserSessions(ctx, uid)
	return s.GetUser(ctx, uid)
}

// SetEmailUnconditional updates an email without a verification round-trip.
// CLI-only; intended for admin shell recovery. Returns ErrUserNotFound when
// oldEmail does not match any row.
func (s *Store) SetEmailUnconditional(ctx context.Context, oldEmail, newEmail string) error {
	if newEmail == "" {
		return errors.New("new_email required")
	}
	tag, err := s.Pool.Exec(ctx,
		`UPDATE users SET email = $2 WHERE lower(email) = lower($1)`, oldEmail, newEmail)
	if err != nil {
		if pgIsUniqueViolation(err) {
			return ErrUserExists
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}
