package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionPrefix      = "mon_sess_"
	fallbackSessionTTL = 12 * time.Hour
	bcryptCost         = 12

	// AUDIT-013: failed-login lockout policy. After this many bad attempts
	// inside the window, the account is locked for the duration. The window
	// slides — clean attempts older than the window from the counter.
	loginMaxFailedAttempts = 5
	loginAttemptWindow     = 15 * time.Minute
	loginLockoutDuration   = 15 * time.Minute
)

var (
	ErrUserExists       = errors.New("user already exists")
	ErrUserNotFound     = errors.New("user not found")
	ErrUserDisabled     = errors.New("user disabled")
	ErrPasswordMismatch = errors.New("password does not match")
	ErrSessionInvalid   = errors.New("session token invalid or expired")
	// ErrUserLockedOut signals AuthenticateUser refused due to a temporary
	// lockout from too many failed attempts (AUDIT-013).
	ErrUserLockedOut = errors.New("account temporarily locked")
)

// FailedLoginAttempts is an in-memory tracker for AUDIT-013. Keyed by
// case-folded email so an attacker cannot toggle the lockout off by varying
// case. Persisted to DB is intentionally out of scope for this commit; the
// admin can simply restart to clear all lockouts in an emergency.
type FailedLoginAttempts struct {
	mu       sync.RWMutex
	attempts map[string][]time.Time
	lockedAt map[string]time.Time
}

// NewFailedLoginAttempts builds an empty tracker.
func NewFailedLoginAttempts() *FailedLoginAttempts {
	return &FailedLoginAttempts{
		attempts: make(map[string][]time.Time),
		lockedAt: make(map[string]time.Time),
	}
}

// failedLoginsSingleton holds the per-process tracker. The package
// initializer guarantees it exists before any Store method is invoked, so
// callers don't need to wire anything up. This is intentional: the spec
// keeps the fix out of the DB schema and contained to a single file.
var failedLoginsSingleton = NewFailedLoginAttempts()

// FailedLogins returns the process-wide failed-login tracker. It is exposed
// as a method (rather than a struct field on Store) so this audit fix
// touches only userauth.go. AUDIT-013.
func (s *Store) failedLogins() *FailedLoginAttempts { return failedLoginsSingleton }

// RecordFailedLogin is the package-level helper documented by the audit
// (AUDIT-013). It delegates to the in-memory tracker so callers don't need
// to reach for the singleton directly.
func (s *Store) RecordFailedLogin(_ context.Context, email string) {
	s.failedLogins().RecordFailedLogin(email)
}

// IsLockedOut reports whether email is currently locked out and, if so, the
// time at which the lock expires.
func (s *Store) IsLockedOut(_ context.Context, email string) (bool, time.Time) {
	return s.failedLogins().IsLockedOut(email)
}

// FailedLogins is the public accessor for the tracker. Tests can use this
// to inspect or reset state.
func (s *Store) FailedLoginsTracker() *FailedLoginAttempts { return s.failedLogins() }

func emailKey(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// IsLockedOut reports whether the account is currently locked, and if so the
// time at which the lock expires. A zero time is returned when not locked.
func (f *FailedLoginAttempts) IsLockedOut(email string) (bool, time.Time) {
	if f == nil {
		return false, time.Time{}
	}
	key := emailKey(email)
	f.mu.Lock()
	defer f.mu.Unlock()
	until, ok := f.lockedAt[key]
	if !ok {
		return false, time.Time{}
	}
	if time.Now().After(until) {
		// Expired — clear so the next failed attempt starts fresh.
		delete(f.lockedAt, key)
		delete(f.attempts, key)
		return false, time.Time{}
	}
	return true, until
}

// RecordFailedLogin appends a failed attempt and engages the lockout if the
// threshold is crossed inside the rolling window.
func (f *FailedLoginAttempts) RecordFailedLogin(email string) {
	if f == nil {
		return
	}
	key := emailKey(email)
	now := time.Now()
	cutoff := now.Add(-loginAttemptWindow)

	f.mu.Lock()
	defer f.mu.Unlock()

	// Drop entries older than the sliding window.
	prev := f.attempts[key]
	kept := prev[:0]
	for _, t := range prev {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	f.attempts[key] = kept

	if len(kept) >= loginMaxFailedAttempts {
		f.lockedAt[key] = now.Add(loginLockoutDuration)
	}
}

// ClearFailedLogins resets the counter and lockout for an email, called on
// successful authentication.
func (f *FailedLoginAttempts) ClearFailedLogins(email string) {
	if f == nil {
		return
	}
	key := emailKey(email)
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.attempts, key)
	delete(f.lockedAt, key)
}

// GC drops per-email buckets whose newest attempt is older than the sliding
// window and lockout entries that have already expired. Without this, an
// attacker iterating distinct email addresses could grow the in-memory map
// without bound. Called periodically by the housekeeping reaper.
//
// To avoid pausing Auth requests for the entire sweep on a million-bucket
// map, GC works in two passes per map:
//  1. Snapshot the candidate keys under a brief RLock (attempts) / Lock
//     (lockedAt — needs write because nothing else is allowed to read mid-
//     pass; we still hold it only for the snapshot duration).
//  2. Walk the snapshot WITHOUT holding the lock. For each candidate, take
//     a brief write lock, re-check that the bucket is still stale, and only
//     then delete. The re-check is essential because RecordFailedLogin /
//     IsLockedOut may have repopulated the bucket between snapshot and
//     delete.
func (f *FailedLoginAttempts) GC() {
	if f == nil {
		return
	}
	now := time.Now()
	cutoff := now.Add(-loginAttemptWindow)

	// Pass 1: stale attempt buckets. Snapshot under read lock, delete
	// per-key under brief write locks with re-validation.
	f.mu.RLock()
	attemptCandidates := make([]string, 0, len(f.attempts))
	for key, ts := range f.attempts {
		if _, locked := f.lockedAt[key]; locked {
			// Locked-out entries are kept so the lockout horizon survives a
			// GC pass; pass 2 will clean them when the lock itself expires.
			continue
		}
		if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
			attemptCandidates = append(attemptCandidates, key)
		}
	}
	f.mu.RUnlock()

	for _, key := range attemptCandidates {
		f.mu.Lock()
		// Re-check under the write lock — the bucket may have been touched
		// in the meantime by RecordFailedLogin or wiped by ClearFailedLogins.
		if _, locked := f.lockedAt[key]; locked {
			f.mu.Unlock()
			continue
		}
		ts, ok := f.attempts[key]
		if ok && (len(ts) == 0 || !ts[len(ts)-1].After(cutoff)) {
			delete(f.attempts, key)
		}
		f.mu.Unlock()
	}

	// Pass 2: expired lockouts. Same shape — snapshot, then delete with
	// per-key re-validation.
	f.mu.RLock()
	lockCandidates := make([]string, 0, len(f.lockedAt))
	for key, until := range f.lockedAt {
		if now.After(until) {
			lockCandidates = append(lockCandidates, key)
		}
	}
	f.mu.RUnlock()

	for _, key := range lockCandidates {
		f.mu.Lock()
		if until, ok := f.lockedAt[key]; ok && now.After(until) {
			delete(f.lockedAt, key)
			delete(f.attempts, key)
		}
		f.mu.Unlock()
	}
}

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
//
// AUDIT-013: the in-memory FailedLoginAttempts tracker is consulted before
// the bcrypt compare. Bad passwords increment the counter; a successful auth
// clears it. While a lockout is in effect we still execute a dummy bcrypt
// compare so a timing observer cannot tell locked from not-locked.
func (s *Store) AuthenticateUser(ctx context.Context, email, password string) (User, error) {
	if tr := s.failedLogins(); tr != nil {
		if locked, _ := tr.IsLockedOut(email); locked {
			// Match dummy work of the not-found path so timing stays uniform.
			_ = bcrypt.CompareHashAndPassword(
				[]byte("$2a$12$DTH4XIQv0vP3AEIp0OPvO.8uDCCO7EM77NMwgVDkdcL3lKkNn7w8a"),
				[]byte(password))
			return User{}, ErrUserLockedOut
		}
	}

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
		// Count the attempt against the supplied email so an attacker
		// guessing a valid user's address cannot hide behind unknown ones.
		s.failedLogins().RecordFailedLogin(email)
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
		s.failedLogins().RecordFailedLogin(email)
		return User{}, ErrPasswordMismatch
	}
	s.failedLogins().ClearFailedLogins(email)
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

// SetPassword unconditionally rewrites the user's bcrypt hash. Used by the
// admin reset-password CLI flag — there is no current-password check, so the
// caller must already have shell-level trust on the box.
func (s *Store) SetPassword(ctx context.Context, email, newPassword string) error {
	if email == "" || newPassword == "" {
		return errors.New("email and password required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx,
		`UPDATE users SET password_hash = $2 WHERE email = $1`, email, string(hash))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
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

// IsLastEnabledAdmin reports whether removing or disabling userID would
// leave the system without any enabled admin. Used by the API layer to
// refuse delete/lock/demote on the final admin.
func (s *Store) IsLastEnabledAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	// "enabled admin" = role=admin AND disabled_at IS NULL AND not the
	// candidate being removed.
	var n int
	err := s.Pool.QueryRow(ctx, `
		SELECT count(*) FROM users
		WHERE role = 'admin' AND disabled_at IS NULL AND id <> $1`, userID,
	).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == 0, nil
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

// effectiveSessionTTL returns the active session TTL, derived from
// SecurityPolicy.MaxSessionHours (admin-tunable). If the caller passes a
// non-zero ttl, it is clamped to the max; otherwise the max itself is used.
// On policy load failure we fall back to fallbackSessionTTL — better a
// stale-but-correct session than refusing to log anyone in.
func (s *Store) effectiveSessionTTL(ctx context.Context, requested time.Duration) time.Duration {
	p, err := s.GetSecurityPolicy(ctx)
	if err != nil {
		return fallbackSessionTTL
	}
	maxTTL := time.Duration(p.MaxSessionHours) * time.Hour
	if maxTTL <= 0 {
		maxTTL = fallbackSessionTTL
	}
	if requested <= 0 || requested > maxTTL {
		return maxTTL
	}
	return requested
}

// IssueSession creates a new session for u and returns the plaintext token.
// userAgent and remoteIP are recorded for audit but never trusted as
// authentication signal.
func (s *Store) IssueSession(ctx context.Context, u User, userAgent, remoteIP string, ttl time.Duration) (string, error) {
	ttl = s.effectiveSessionTTL(ctx, ttl)
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

	if _, aerr := s.Pool.Exec(ctx,
		`INSERT INTO audit_log (actor, action, target, detail) VALUES ($1, 'auth.session.issued', $2, $3)`,
		"user:"+u.Email, u.ID.String(),
		map[string]any{"ttl_hours": int(ttl / time.Hour), "user_agent": userAgent, "remote_ip": remoteIP},
	); aerr != nil {
		slog.Warn("audit_log insert (session.issued)", "err", aerr)
	}

	return plaintext, nil
}

// ValidateSession looks up a session by token, bumps last_seen_at, and
// returns the owning user.
func (s *Store) ValidateSession(ctx context.Context, token string) (User, error) {
	hash := hashSecret(token)

	// Policy lookup is best-effort: a failure here falls back to 0, which the
	// SQL below treats as "no idle limit". We'd rather honor a stale max
	// session TTL than lock everyone out when settings briefly misbehave.
	p, _ := s.GetSecurityPolicy(ctx)

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
		  AND ($2 = 0 OR user_sessions.last_seen_at IS NULL OR user_sessions.last_seen_at > now() - make_interval(mins => $2))
		RETURNING users.id, users.email, users.role, users.created_at`,
		hash, p.IdleTimeoutMinutes).Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt)
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
