package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/pr0ph37/mon/internal/server/notify"
	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

var (
	ErrChannelNotFound = errors.New("notification channel not found")
)

func (s *Store) ListChannels(ctx context.Context) ([]apitypes.NotificationChannel, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, type, name, enabled, config, created_at,
		       COALESCE(created_by, ''), last_used_at, COALESCE(last_error, '')
		FROM notification_channels
		ORDER BY name`)
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

// CreateChannel inserts a new channel. Type and name combined are unique.
func (s *Store) CreateChannel(ctx context.Context, in apitypes.NotificationChannelInput, createdBy string) (apitypes.NotificationChannel, error) {
	if in.Type == "" || in.Name == "" {
		return apitypes.NotificationChannel{}, errors.New("type and name required")
	}
	cfg, err := json.Marshal(orEmptyAny(in.Config))
	if err != nil {
		return apitypes.NotificationChannel{}, err
	}
	var id uuid.UUID
	var createdAt time.Time
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO notification_channels (type, name, enabled, config, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`,
		in.Type, in.Name, in.Enabled, cfg, nullableString(createdBy),
	).Scan(&id, &createdAt)
	if err != nil {
		if pgIsUniqueViolation(err) {
			return apitypes.NotificationChannel{}, errors.New("a channel with this type+name already exists")
		}
		return apitypes.NotificationChannel{}, fmt.Errorf("channel insert: %w", err)
	}
	c, err := s.GetChannel(ctx, id)
	return c, err
}

func (s *Store) UpdateChannel(ctx context.Context, id uuid.UUID, in apitypes.NotificationChannelInput) (apitypes.NotificationChannel, error) {
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

func (s *Store) DeleteChannel(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM notification_channels WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
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
		       COALESCE(created_by, ''), last_used_at, COALESCE(last_error, '')
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
		cfg        []byte
		lastUsedAt *time.Time
	)
	if err := scan(&idVal, &c.Type, &c.Name, &c.Enabled, &cfg, &c.CreatedAt,
		&c.CreatedBy, &lastUsedAt, &c.LastError); err != nil {
		return c, err
	}
	c.ID = idVal.String()
	c.Config = map[string]any{}
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &c.Config)
	}
	c.LastUsedAt = lastUsedAt
	return c, nil
}

// redactSecrets blanks sensitive fields before returning a channel to the API.
// We never want to surface SMTP passwords, webhook URLs, or ntfy tokens to a
// caller listing channels, even an admin. To rotate, the operator updates the
// channel.
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
