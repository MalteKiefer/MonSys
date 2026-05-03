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
	ID        uuid.UUID
	Email     string
	Role      string
	CreatedAt time.Time
	Disabled  bool
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

// AuthenticateUser verifies email+password and returns the user.
func (s *Store) AuthenticateUser(ctx context.Context, email, password string) (User, error) {
	var (
		u            User
		passwordHash string
		disabledAt   *time.Time
	)
	err := s.Pool.QueryRow(ctx, `
		SELECT id, email, role, password_hash, created_at, disabled_at
		FROM users WHERE lower(email) = lower($1)`,
		email).Scan(&u.ID, &u.Email, &u.Role, &passwordHash, &u.CreatedAt, &disabledAt)
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
	return u, nil
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
