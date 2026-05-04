package store

import (
	"context"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// GetNotificationSettings returns the singleton row. Migration 0019 inserts
// the default row, so callers can rely on this never returning ErrNoRows in
// a properly migrated database.
func (s *Store) GetNotificationSettings(ctx context.Context) (apitypes.NotificationSettings, error) {
	var out apitypes.NotificationSettings
	var days []int16
	err := s.Pool.QueryRow(ctx, `
		SELECT quiet_enabled, quiet_start, quiet_end, quiet_days, quiet_tz,
		       updated_at, COALESCE(updated_by, '')
		FROM notification_settings WHERE id = 1`,
	).Scan(
		&out.QuietEnabled, &out.QuietStart, &out.QuietEnd, &days, &out.QuietTZ,
		&out.UpdatedAt, &out.UpdatedBy,
	)
	if err != nil {
		return apitypes.NotificationSettings{}, err
	}
	out.QuietDays = make([]int, 0, len(days))
	for _, d := range days {
		out.QuietDays = append(out.QuietDays, int(d))
	}
	return out, nil
}

// UpsertNotificationSettings saves the singleton row. The migration seeds
// id=1, so this is effectively an UPDATE — the ON CONFLICT branch covers
// fresh installs where someone managed to delete the row.
func (s *Store) UpsertNotificationSettings(ctx context.Context, in apitypes.NotificationSettingsInput, updatedBy string) (apitypes.NotificationSettings, error) {
	if in.QuietTZ == "" {
		in.QuietTZ = "UTC"
	}
	if in.QuietStart == "" {
		in.QuietStart = "22:00"
	}
	if in.QuietEnd == "" {
		in.QuietEnd = "06:00"
	}
	// Coerce to int16 for SMALLINT[].
	days := make([]int16, 0, len(in.QuietDays))
	for _, d := range in.QuietDays {
		if d < 0 || d > 6 {
			continue
		}
		days = append(days, int16(d))
	}

	_, err := s.Pool.Exec(ctx, `
		INSERT INTO notification_settings (
			id, quiet_enabled, quiet_start, quiet_end, quiet_days, quiet_tz,
			updated_at, updated_by
		)
		VALUES (1, $1, $2, $3, $4, $5, now(), $6)
		ON CONFLICT (id) DO UPDATE SET
			quiet_enabled = EXCLUDED.quiet_enabled,
			quiet_start   = EXCLUDED.quiet_start,
			quiet_end     = EXCLUDED.quiet_end,
			quiet_days    = EXCLUDED.quiet_days,
			quiet_tz      = EXCLUDED.quiet_tz,
			updated_at    = now(),
			updated_by    = EXCLUDED.updated_by`,
		in.QuietEnabled, in.QuietStart, in.QuietEnd, days, in.QuietTZ,
		nullableString(updatedBy),
	)
	if err != nil {
		return apitypes.NotificationSettings{}, err
	}
	return s.GetNotificationSettings(ctx)
}
