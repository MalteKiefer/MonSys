package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// avatar caps — refuse anything larger or with a non-image type at the API
// edge, but enforce here too as defence in depth.
const (
	// MaxAvatarBytes is the cap for stored avatar bytes. We re-encode
	// aggressively on the frontend, so 512 KiB is plenty for a 200x200 PNG.
	MaxAvatarBytes = 512 * 1024
)

var (
	// ErrAvatarNotFound signals the user has no avatar bytes stored. Callers
	// map this to HTTP 404.
	ErrAvatarNotFound = errors.New("avatar not found")
	// ErrAvatarTooBig signals the payload exceeds MaxAvatarBytes.
	ErrAvatarTooBig = errors.New("avatar exceeds size limit")
)

// SetAvatar stores raw image bytes on the users row. content_type must be
// one of "image/png", "image/jpeg", "image/webp" — caller validates and
// strips EXIF before passing here.
func (s *Store) SetAvatar(ctx context.Context, userID uuid.UUID, contentType string, b []byte) error {
	if len(b) == 0 {
		return errors.New("avatar empty")
	}
	if len(b) > MaxAvatarBytes {
		return ErrAvatarTooBig
	}
	tag, err := s.Pool.Exec(ctx,
		`UPDATE users SET avatar_bytes = $2, avatar_content_type = $3, avatar_updated_at = now() WHERE id = $1`,
		userID, b, contentType,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// GetAvatar returns the stored bytes + content_type for the user. Errors
// ErrAvatarNotFound when the column is NULL — caller maps to 404.
func (s *Store) GetAvatar(ctx context.Context, userID uuid.UUID) ([]byte, string, error) {
	var (
		b           []byte
		contentType *string
	)
	err := s.Pool.QueryRow(ctx,
		`SELECT avatar_bytes, avatar_content_type FROM users WHERE id = $1`,
		userID,
	).Scan(&b, &contentType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrUserNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if len(b) == 0 {
		return nil, "", ErrAvatarNotFound
	}
	ct := "image/png"
	if contentType != nil && *contentType != "" {
		ct = *contentType
	}
	return b, ct, nil
}

// DeleteAvatar wipes the columns. Idempotent — returns nil even if there
// was nothing to delete.
func (s *Store) DeleteAvatar(ctx context.Context, userID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE users SET avatar_bytes = NULL, avatar_content_type = NULL, avatar_updated_at = NULL WHERE id = $1`,
		userID,
	)
	return err
}

// GetAvatarMeta returns whether the user has an avatar set and, if so, when
// it was last updated. Used by the /v1/auth/me response so the UI knows
// whether to render the placeholder or fetch the bytes.
func (s *Store) GetAvatarMeta(ctx context.Context, userID uuid.UUID) (bool, *time.Time, error) {
	var (
		hasAvatar bool
		updatedAt *time.Time
	)
	err := s.Pool.QueryRow(ctx,
		`SELECT avatar_bytes IS NOT NULL, avatar_updated_at FROM users WHERE id = $1`,
		userID,
	).Scan(&hasAvatar, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, ErrUserNotFound
	}
	if err != nil {
		return false, nil, err
	}
	return hasAvatar, updatedAt, nil
}
