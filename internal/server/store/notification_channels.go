package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pr0ph37/mon/internal/server/notify"
	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

var (
	ErrChannelNotFound = errors.New("notification channel not found")
)

// ListChannels returns channels visible to the caller. Admins see every
// channel (own + others' user-owned + shared). Non-admin users see their own
// channels plus shared channels (owner_user_id IS NULL).
func (s *Store) ListChannels(ctx context.Context, callerID uuid.UUID, isAdmin bool) ([]apitypes.NotificationChannel, error) {
	var rows pgx.Rows
	var err error
	if isAdmin {
		rows, err = s.Pool.Query(ctx, `
			SELECT id, type, name, enabled, config, created_at,
			       COALESCE(created_by, ''), owner_user_id,
			       last_used_at, COALESCE(last_error, '')
			FROM notification_channels
			ORDER BY name`)
	} else {
		rows, err = s.Pool.Query(ctx, `
			SELECT id, type, name, enabled, config, created_at,
			       COALESCE(created_by, ''), owner_user_id,
			       last_used_at, COALESCE(last_error, '')
			FROM notification_channels
			WHERE owner_user_id = $1 OR owner_user_id IS NULL
			ORDER BY name`, callerID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.NotificationChannel{}
	for rows.Next() {
		c, err := scanChannel(rows.Scan)
		if err != nil {
			return nil, err
		}
		redactSecrets(&c)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetChannel(ctx context.Context, id uuid.UUID) (apitypes.NotificationChannel, error) {
	c, err := s.fetchChannelRaw(ctx, id)
	if err != nil {
		return c, err
	}
	redactSecrets(&c)
	return c, nil
}

// CreateChannel inserts a new channel. owner is nil for shared/admin-only
// channels (used for SMTP); otherwise the channel is private to the user.
func (s *Store) CreateChannel(ctx context.Context, in apitypes.NotificationChannelInput, createdBy string, owner *uuid.UUID) (apitypes.NotificationChannel, error) {
	if in.Type == "" || in.Name == "" {
		return apitypes.NotificationChannel{}, errors.New("type and name required")
	}
	cfg, err := json.Marshal(orEmptyAny(in.Config))
	if err != nil {
		return apitypes.NotificationChannel{}, err
	}
	var ownerArg any
	if owner != nil {
		ownerArg = *owner
	}
	var id uuid.UUID
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO notification_channels (type, name, enabled, config, created_by, owner_user_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		in.Type, in.Name, in.Enabled, cfg, nullableString(createdBy), ownerArg,
	).Scan(&id)
	if err != nil {
		if pgIsUniqueViolation(err) {
			return apitypes.NotificationChannel{}, errors.New("a channel with this type+name already exists")
		}
		return apitypes.NotificationChannel{}, fmt.Errorf("channel insert: %w", err)
	}
	c, err := s.GetChannel(ctx, id)
	return c, err
}

// UpdateChannel only succeeds when the caller owns the channel or is admin.
func (s *Store) UpdateChannel(ctx context.Context, id uuid.UUID, in apitypes.NotificationChannelInput, callerID uuid.UUID, isAdmin bool) (apitypes.NotificationChannel, error) {
	if !isAdmin {
		if err := s.assertOwner(ctx, id, callerID); err != nil {
			return apitypes.NotificationChannel{}, err
		}
	}
	cfg, err := json.Marshal(orEmptyAny(in.Config))
	if err != nil {
		return apitypes.NotificationChannel{}, err
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE notification_channels
		SET type = $2, name = $3, enabled = $4, config = $5
		WHERE id = $1`,
		id, in.Type, in.Name, in.Enabled, cfg)
	if err != nil {
		if pgIsUniqueViolation(err) {
			return apitypes.NotificationChannel{}, errors.New("a channel with this type+name already exists")
		}
		return apitypes.NotificationChannel{}, fmt.Errorf("channel update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apitypes.NotificationChannel{}, ErrChannelNotFound
	}
	return s.GetChannel(ctx, id)
}

func (s *Store) DeleteChannel(ctx context.Context, id uuid.UUID, callerID uuid.UUID, isAdmin bool) error {
	if !isAdmin {
		if err := s.assertOwner(ctx, id, callerID); err != nil {
			return err
		}
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM notification_channels WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrChannelNotFound
	}
	return nil
}

// assertOwner returns ErrChannelNotFound when the channel does not exist or
// is not owned by callerID. We deliberately conflate "not found" and
// "not yours" so a non-admin can't probe foreign channel ids.
func (s *Store) assertOwner(ctx context.Context, id uuid.UUID, callerID uuid.UUID) error {
	var owner *uuid.UUID
	err := s.Pool.QueryRow(ctx,
		`SELECT owner_user_id FROM notification_channels WHERE id = $1`, id,
	).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrChannelNotFound
	}
	if err != nil {
		return err
	}
	if owner == nil || *owner != callerID {
		return ErrChannelNotFound
	}
	return nil
}

// SendChannel dispatches m via channel id and updates last_used_at / last_error.
func (s *Store) SendChannel(ctx context.Context, id uuid.UUID, m notify.Message) error {
	c, err := s.fetchChannelRaw(ctx, id)
	if err != nil {
		return err
	}
	if !c.Enabled {
		return errors.New("channel is disabled")
	}
	sendErr := notify.Dispatch(ctx, notify.Channel{
		ID:     c.ID,
		Type:   c.Type,
		Name:   c.Name,
		Config: c.Config,
	}, m)

	// Best-effort error logging back to the row. Don't shadow the original error.
	if sendErr != nil {
		_, _ = s.Pool.Exec(ctx,
			`UPDATE notification_channels SET last_error = $2 WHERE id = $1`,
			id, truncate(sendErr.Error(), 500))
	} else {
		_, _ = s.Pool.Exec(ctx,
			`UPDATE notification_channels SET last_used_at = now(), last_error = NULL WHERE id = $1`,
			id)
	}
	return sendErr
}

// fetchChannelRaw returns the unredacted record for dispatch.
func (s *Store) fetchChannelRaw(ctx context.Context, id uuid.UUID) (apitypes.NotificationChannel, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, type, name, enabled, config, created_at,
		       COALESCE(created_by, ''), owner_user_id,
		       last_used_at, COALESCE(last_error, '')
		FROM notification_channels WHERE id = $1`, id)
	if err != nil {
		return apitypes.NotificationChannel{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return apitypes.NotificationChannel{}, ErrChannelNotFound
	}
	return scanChannel(rows.Scan)
}

func scanChannel(scan func(...any) error) (apitypes.NotificationChannel, error) {
	var (
		c          apitypes.NotificationChannel
		idVal      uuid.UUID
		ownerVal   *uuid.UUID
		cfg        []byte
		lastUsedAt *time.Time
	)
	if err := scan(&idVal, &c.Type, &c.Name, &c.Enabled, &cfg, &c.CreatedAt,
		&c.CreatedBy, &ownerVal, &lastUsedAt, &c.LastError); err != nil {
		return c, err
	}
	c.ID = idVal.String()
	c.Config = map[string]any{}
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &c.Config)
	}
	if ownerVal != nil {
		c.OwnerUserID = ownerVal.String()
	}
	c.LastUsedAt = lastUsedAt
	return c, nil
}

// redactSecrets blanks sensitive fields before returning a channel to the API.
// We never want to surface SMTP passwords, webhook URLs, or ntfy tokens to a
// caller listing channels, even an admin. To rotate, the operator updates the
// channel. Note: Discord and Slack/Mattermost all use webhook_url, so the
// same key covers them.
func redactSecrets(c *apitypes.NotificationChannel) {
	if c.Config == nil {
		return
	}
	for _, k := range []string{"password", "webhook_url", "auth_token"} {
		if _, ok := c.Config[k]; ok {
			c.Config[k] = "***"
		}
	}
}

func orEmptyAny(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
