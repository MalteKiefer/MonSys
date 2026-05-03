package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionPrefix      = "mon_sess_"
	defaultSessionTTL  = 12 * time.Hour
	bcryptCost         = 12
)

var (
	ErrUserExists       = errors.New("user already exists")
	ErrUserNotFound     = errors.New("user not found")
	ErrUserDisabled     = errors.New("user disabled")
	ErrPasswordMismatch = errors.New("password does not match")
	ErrSessionInvalid   = errors.New("session token invalid or expired")
)

// User mirrors the `users` row a session-aware caller actually needs. We
// deliberately don't expose password_hash here.
type User struct {
	ID         uuid.UUID
	Email      string
	Role       string
	CreatedAt  time.Time
	Disabled   bool
	TOTPActive bool
}

// CreateUser inserts a new user with bcrypt-hashed password. role is "admin"
// or "user"; the API treats anything non-"admin" as read-only.
func (s *Store) CreateUser(ctx context.Context, email, password, role string) (User, error) {
	if email == "" || password == "" {
		return User{}, errors.New("email and password required")
	}
	if role == "" {
		role = "user"
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return User{}, fmt.Errorf("bcrypt: %w", err)
	}

	var u User
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id, email, role, created_at`,
		email, string(hash), role,
	).Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt)
	if err != nil {
		// pg unique_violation = 23505; we don't import the constant to keep
		// pgconn out of the import graph.
		if pgIsUniqueViolation(err) {
			return User{}, ErrUserExists
		}
		return User{}, fmt.Errorf("user insert: %w", err)
	}
	return u, nil
}

// AuthenticateUser verifies email+password and returns the user. The
// returned User has TOTPActive populated so callers can decide whether to
// continue with the 2FA challenge step.
func (s *Store) AuthenticateUser(ctx context.Context, email, password string) (User, error) {
	var (
		u            User
		passwordHash string
		disabledAt   *time.Time
		totpEnabled  *time.Time
	)
	err := s.Pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.role, u.password_hash, u.created_at, u.disabled_at,
		       t.enabled_at
		FROM users u
		LEFT JOIN user_totp t ON t.user_id = u.id
		WHERE lower(u.email) = lower($1)`,
		email).Scan(&u.ID, &u.Email, &u.Role, &passwordHash, &u.CreatedAt, &disabledAt, &totpEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		// Run a dummy compare anyway so the error path takes the same
		// time as the success path. Mitigates user enumeration via timing.
		_ = bcrypt.CompareHashAndPassword(
			[]byte("$2a$12$DTH4XIQv0vP3AEIp0OPvO.8uDCCO7EM77NMwgVDkdcL3lKkNn7w8a"),
			[]byte(password))
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("user lookup: %w", err)
	}
	if disabledAt != nil {
		u.Disabled = true
		return u, ErrUserDisabled
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return User{}, ErrPasswordMismatch
	}
	u.TOTPActive = totpEnabled != nil
	return u, nil
}

// GetUser fetches a user record by id.
func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (User, error) {
	var (
		u           User
		disabledAt  *time.Time
		totpEnabled *time.Time
	)
	err := s.Pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.role, u.created_at, u.disabled_at, t.enabled_at
		FROM users u
		LEFT JOIN user_totp t ON t.user_id = u.id
		WHERE u.id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt, &disabledAt, &totpEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.Disabled = disabledAt != nil
	u.TOTPActive = totpEnabled != nil
	return u, nil
}

// ChangePassword verifies the current password and writes a new bcrypt hash.
// Returns ErrPasswordMismatch if the current password is wrong. The new
// password is policy-checked by the caller (api layer) to keep this layer
// dumb about UX rules.
func (s *Store) ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string) error {
	var hash string
	err := s.Pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1`, userID,
	).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(currentPassword)); err != nil {
		return ErrPasswordMismatch
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx,
		`UPDATE users SET password_hash = $2 WHERE id = $1`, userID, string(newHash))
	return err
}

// ChangeEmail verifies the user's password and updates email. Returns
// ErrUserExists if the new email is taken.
func (s *Store) ChangeEmail(ctx context.Context, userID uuid.UUID, password, newEmail string) error {
	if newEmail == "" {
		return errors.New("new_email required")
	}
	var hash string
	err := s.Pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1`, userID,
	).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return ErrPasswordMismatch
	}
	_, err = s.Pool.Exec(ctx,
		`UPDATE users SET email = $2 WHERE id = $1`, userID, newEmail)
	if err != nil {
		if pgIsUniqueViolation(err) {
			return ErrUserExists
		}
		return err
	}
	return nil
}

// SetPasswordByAdmin hashes and stores a new password without requiring the
// current one. Used by admin reset and by ConsumeResetToken.
func (s *Store) SetPasswordByAdmin(ctx context.Context, userID uuid.UUID, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx,
		`UPDATE users SET password_hash = $2 WHERE id = $1`, userID, string(hash))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetUserDisabled flips the disabled_at field. Used to lock/unlock accounts.
func (s *Store) SetUserDisabled(ctx context.Context, userID uuid.UUID, disabled bool) error {
	var query string
	if disabled {
		query = `UPDATE users SET disabled_at = COALESCE(disabled_at, now()) WHERE id = $1`
	} else {
		query = `UPDATE users SET disabled_at = NULL WHERE id = $1`
	}
	tag, err := s.Pool.Exec(ctx, query, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetUserRole updates a user's role.
func (s *Store) SetUserRole(ctx context.Context, userID uuid.UUID, role string) error {
	if role != "admin" && role != "user" {
		return errors.New("role must be admin or user")
	}
	tag, err := s.Pool.Exec(ctx,
		`UPDATE users SET role = $2 WHERE id = $1`, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// DeleteUser removes a user. Sessions and TOTP state cascade.
func (s *Store) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// AdminUserSummary mirrors apitypes.AdminUserSummary; the store builds it
// directly via a join so callers don't have to issue per-user follow-up
// queries.
type AdminUserSummary struct {
	ID          uuid.UUID
	Email       string
	Role        string
	CreatedAt   time.Time
	DisabledAt  *time.Time
	TOTPActive  bool
	LastLoginAt *time.Time
}

func (s *Store) ListUsers(ctx context.Context) ([]AdminUserSummary, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT u.id, u.email, u.role, u.created_at, u.disabled_at,
		       t.enabled_at IS NOT NULL,
		       (SELECT max(last_seen_at) FROM user_sessions WHERE user_id = u.id) AS last_login_at
		FROM users u
		LEFT JOIN user_totp t ON t.user_id = u.id
		ORDER BY u.email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminUserSummary{}
	for rows.Next() {
		var u AdminUserSummary
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt,
			&u.DisabledAt, &u.TOTPActive, &u.LastLoginAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// IssueSession creates a new session for u and returns the plaintext token.
// userAgent and remoteIP are recorded for audit but never trusted as
// authentication signal.
func (s *Store) IssueSession(ctx context.Context, u User, userAgent, remoteIP string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	plaintext, err := generateSecret(sessionPrefix)
	if err != nil {
		return "", err
	}
	hash := hashSecret(plaintext) // sha256 — fine here, token has 256 bits of entropy
	expires := time.Now().Add(ttl).UTC()

	_, err = s.Pool.Exec(ctx, `
		INSERT INTO user_sessions (user_id, token_hash, user_agent, remote_ip, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		u.ID, hash, nullableString(userAgent), nullableString(remoteIP), expires)
	if err != nil {
		return "", fmt.Errorf("session insert: %w", err)
	}
	return plaintext, nil
}

// ValidateSession looks up a session by token, bumps last_seen_at, and
// returns the owning user.
func (s *Store) ValidateSession(ctx context.Context, token string) (User, error) {
	hash := hashSecret(token)

	var u User
	err := s.Pool.QueryRow(ctx, `
		UPDATE user_sessions
		SET last_seen_at = now()
		FROM users
		WHERE user_sessions.token_hash = $1
		  AND user_sessions.expires_at > now()
		  AND user_sessions.revoked_at IS NULL
		  AND users.id = user_sessions.user_id
		  AND users.disabled_at IS NULL
		RETURNING users.id, users.email, users.role, users.created_at`,
		hash).Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrSessionInvalid
	}
	if err != nil {
		return User{}, fmt.Errorf("session lookup: %w", err)
	}
	return u, nil
}

// RevokeSession marks a session revoked. Idempotent.
func (s *Store) RevokeSession(ctx context.Context, token string) error {
	hash := hashSecret(token)
	_, err := s.Pool.Exec(ctx,
		`UPDATE user_sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`,
		hash)
	return err
}

// pgIsUniqueViolation matches by error text to avoid importing pgconn just
// for one error code constant. Postgres error text "duplicate key value
// violates unique constraint" / SQLSTATE 23505 is stable.
func pgIsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key value violates unique constraint") ||
		strings.Contains(msg, "23505")
}
