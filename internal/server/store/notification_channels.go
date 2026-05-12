package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MalteKiefer/MonSys/internal/server/notify"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

var ErrChannelNotFound = errors.New("notification channel not found")

// ListChannels returns channels visible to the caller. Admins see every
// channel (own + others' user-owned + shared). Non-admin users see their own
// channels plus shared channels (owner_user_id IS NULL).
func (s *Store) ListChannels(ctx context.Context, callerID uuid.UUID, isAdmin bool) ([]apitypes.NotificationChannel, error) {
	var rows pgx.Rows
	var err error
	if isAdmin {
		rows, err = s.Pool.Query(ctx, `
			SELECT id, type, name, enabled, config, COALESCE(recipient_email, ''),
			       created_at, COALESCE(created_by, ''), owner_user_id,
			       last_used_at, COALESCE(last_error, '')
			FROM notification_channels
			ORDER BY name`)
	} else {
		rows, err = s.Pool.Query(ctx, `
			SELECT id, type, name, enabled, config, COALESCE(recipient_email, ''),
			       created_at, COALESCE(created_by, ''), owner_user_id,
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

// CreateChannel inserts a new channel. owner is nil for shared channels;
// otherwise the channel is private to the user.
func (s *Store) CreateChannel(ctx context.Context, in apitypes.NotificationChannelInput, createdBy string, owner *uuid.UUID) (apitypes.NotificationChannel, error) {
	if in.Type == "" || in.Name == "" {
		return apitypes.NotificationChannel{}, errors.New("type and name required")
	}
	configForType, recipientForType, err := normalizeChannelInput(in)
	if err != nil {
		return apitypes.NotificationChannel{}, err
	}
	cfg, err := json.Marshal(configForType)
	if err != nil {
		return apitypes.NotificationChannel{}, err
	}
	var ownerArg any
	if owner != nil {
		ownerArg = *owner
	}
	var recipientArg any
	if recipientForType != "" {
		recipientArg = recipientForType
	}
	var id uuid.UUID
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO notification_channels (type, name, enabled, config, recipient_email, created_by, owner_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		in.Type, in.Name, in.Enabled, cfg, recipientArg, nullableString(createdBy), ownerArg,
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
	configForType, recipientForType, err := normalizeChannelInput(in)
	if err != nil {
		return apitypes.NotificationChannel{}, err
	}
	cfg, err := json.Marshal(configForType)
	if err != nil {
		return apitypes.NotificationChannel{}, err
	}
	var recipientArg any
	if recipientForType != "" {
		recipientArg = recipientForType
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE notification_channels
		SET type = $2, name = $3, enabled = $4, config = $5, recipient_email = $6
		WHERE id = $1`,
		id, in.Type, in.Name, in.Enabled, cfg, recipientArg)
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
// For email-typed channels, the global SMTP settings are merged in just before
// dispatch — the channel itself only carries the recipient address.
func (s *Store) SendChannel(ctx context.Context, id uuid.UUID, m notify.Message) error {
	c, err := s.fetchChannelRaw(ctx, id)
	if err != nil {
		return err
	}
	if !c.Enabled {
		return errors.New("channel is disabled")
	}

	dispatchConfig := c.Config
	if c.Type == "email" || c.Type == "smtp" {
		settings, sErr := s.GetSmtpSettings(ctx)
		if sErr != nil {
			return sErr
		}
		if c.RecipientEmail == "" {
			return errors.New("channel has no recipient email")
		}
		dispatchConfig = mergeSmtpDispatchConfig(settings, c.RecipientEmail)
	}

	sendErr := notify.Dispatch(ctx, notify.Channel{
		ID:     c.ID,
		Type:   c.Type,
		Name:   c.Name,
		Config: dispatchConfig,
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
		SELECT id, type, name, enabled, config, COALESCE(recipient_email, ''),
		       created_at, COALESCE(created_by, ''), owner_user_id,
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
	if err := scan(&idVal, &c.Type, &c.Name, &c.Enabled, &cfg, &c.RecipientEmail,
		&c.CreatedAt, &c.CreatedBy, &ownerVal, &lastUsedAt, &c.LastError); err != nil {
		return c, err
	}
	c.ID = idVal.String()
	c.Config = map[string]any{}
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &c.Config); err != nil {
			slog.Warn("notification_channels: corrupt config jsonb", "channel_id", c.ID, "err", err)
			c.Config = map[string]any{}
		}
	}
	if ownerVal != nil {
		c.OwnerUserID = ownerVal.String()
	}
	c.LastUsedAt = lastUsedAt
	return c, nil
}

// redactSecrets blanks sensitive fields before returning a channel to the API.
// We never want to surface webhook URLs or ntfy tokens to a caller listing
// channels, even an admin. To rotate, the operator updates the channel.
// Note: Discord and Slack/Mattermost all use webhook_url, so the same key
// covers them. SMTP passwords live in smtp_settings, not the channel.
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

// normalizeChannelInput enforces type-specific input shape. For type=email,
// the per-channel config is wiped (SMTP transport comes from smtp_settings)
// and recipient_email is required. Other types keep their config map and
// must not carry recipient_email.
func normalizeChannelInput(in apitypes.NotificationChannelInput) (map[string]any, string, error) {
	switch in.Type {
	case "email":
		if in.RecipientEmail == "" {
			return nil, "", errors.New("recipient_email is required for type=email")
		}
		return map[string]any{}, in.RecipientEmail, nil
	case "slack", "mattermost", "discord", "ntfy":
		return orEmptyAny(in.Config), "", nil
	default:
		return nil, "", fmt.Errorf("unsupported channel type %q", in.Type)
	}
}

// mergeSmtpDispatchConfig builds the runtime config map that notify.SMTP
// expects (host/port/auth/from/to/...). Called only for email-typed channels
// at dispatch time; the persisted config row stays empty.
func mergeSmtpDispatchConfig(s SmtpSettingsRaw, recipient string) map[string]any {
	return map[string]any{
		"host":                 s.Host,
		"port":                 s.Port,
		"username":             s.Username,
		"password":             s.Password,
		"from":                 s.FromAddress,
		"to":                   []string{recipient},
		"starttls":             s.StartTLS,
		"tls":                  s.TLS,
		"insecure_skip_verify": s.InsecureSkipVerify,
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
