package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

// ErrSmtpNotConfigured is returned when callers try to use the global SMTP
// transport before an admin has saved settings.
var ErrSmtpNotConfigured = errors.New("smtp settings not configured")

// SmtpSettingsRaw is the unredacted view used internally by the dispatch
// layer. The HTTP API never returns the password — wrap with ToAPI for that.
type SmtpSettingsRaw struct {
	apitypes.SmtpSettings
	Password string
}

// GetSmtpSettings returns the singleton row. ErrSmtpNotConfigured when no
// admin has saved settings yet.
func (s *Store) GetSmtpSettings(ctx context.Context) (SmtpSettingsRaw, error) {
	var out SmtpSettingsRaw
	err := s.Pool.QueryRow(ctx, `
		SELECT host, port, username, password, from_address,
		       starttls, tls, insecure_skip_verify,
		       updated_at, COALESCE(updated_by, '')
		FROM smtp_settings WHERE id = 1`,
	).Scan(
		&out.Host, &out.Port, &out.Username, &out.Password, &out.FromAddress,
		&out.StartTLS, &out.TLS, &out.InsecureSkipVerify,
		&out.UpdatedAt, &out.UpdatedBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SmtpSettingsRaw{}, ErrSmtpNotConfigured
	}
	if err != nil {
		return SmtpSettingsRaw{}, err
	}
	out.HasPassword = out.Password != ""
	return out, nil
}

// UpsertSmtpSettings saves the singleton SMTP row. When in.Password is empty
// and clearPassword is false, the stored password is preserved atomically
// inside the upsert — no read-modify-write race against concurrent admins.
func (s *Store) UpsertSmtpSettings(ctx context.Context, in apitypes.SmtpSettingsInput, updatedBy string) (apitypes.SmtpSettings, error) {
	if in.Host == "" || in.FromAddress == "" {
		return apitypes.SmtpSettings{}, errors.New("host and from_address are required")
	}
	if in.Port <= 0 {
		in.Port = 587
	}

	// Three-valued password handling:
	//   ClearPassword=true  → store empty
	//   Password != ""      → store the new value
	//   else (default)      → keep the previously stored value via ON CONFLICT CASE
	var password string
	keepExisting := !in.ClearPassword && in.Password == ""
	if !keepExisting {
		password = in.Password
	}

	_, err := s.Pool.Exec(ctx, `
		INSERT INTO smtp_settings (
			id, host, port, username, password, from_address,
			starttls, tls, insecure_skip_verify, updated_at, updated_by
		)
		VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, now(), $9)
		ON CONFLICT (id) DO UPDATE SET
			host = EXCLUDED.host,
			port = EXCLUDED.port,
			username = EXCLUDED.username,
			password = CASE WHEN $10 THEN smtp_settings.password ELSE EXCLUDED.password END,
			from_address = EXCLUDED.from_address,
			starttls = EXCLUDED.starttls,
			tls = EXCLUDED.tls,
			insecure_skip_verify = EXCLUDED.insecure_skip_verify,
			updated_at = now(),
			updated_by = EXCLUDED.updated_by`,
		in.Host, in.Port, in.Username, password, in.FromAddress,
		in.StartTLS, in.TLS, in.InsecureSkipVerify, nullableString(updatedBy),
		keepExisting,
	)
	if err != nil {
		return apitypes.SmtpSettings{}, err
	}

	saved, err := s.GetSmtpSettings(ctx)
	if err != nil {
		return apitypes.SmtpSettings{}, err
	}
	return saved.SmtpSettings, nil
}
