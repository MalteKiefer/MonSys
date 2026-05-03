package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pr0ph37/mon/internal/server/probe"
	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

var ErrMonitorNotFound = errors.New("monitor not found")

func (s *Store) ListMonitors(ctx context.Context) ([]apitypes.Monitor, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, type, name, target, params, interval_sec, enabled,
		       created_at, COALESCE(created_by,''),
		       last_check_at, COALESCE(last_status,''),
		       COALESCE(last_latency_ms,0), COALESCE(last_detail,'')
		FROM monitors ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.Monitor{}
	for rows.Next() {
		m, err := scanMonitor(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMonitor(ctx context.Context, id uuid.UUID) (apitypes.Monitor, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, type, name, target, params, interval_sec, enabled,
		       created_at, COALESCE(created_by,''),
		       last_check_at, COALESCE(last_status,''),
		       COALESCE(last_latency_ms,0), COALESCE(last_detail,'')
		FROM monitors WHERE id = $1`, id)
	if err != nil {
		return apitypes.Monitor{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return apitypes.Monitor{}, ErrMonitorNotFound
	}
	return scanMonitor(rows.Scan)
}

func (s *Store) CreateMonitor(ctx context.Context, in apitypes.MonitorInput, createdBy string) (apitypes.Monitor, error) {
	if in.Type == "" || in.Name == "" || in.Target == "" {
		return apitypes.Monitor{}, errors.New("type, name, target required")
	}
	if in.IntervalSec <= 0 {
		in.IntervalSec = 60
	}
	params, err := json.Marshal(orEmptyAny(in.Params))
	if err != nil {
		return apitypes.Monitor{}, err
	}
	var id uuid.UUID
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO monitors (type, name, target, params, interval_sec, enabled, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id`,
		in.Type, in.Name, in.Target, params, in.IntervalSec, in.Enabled, nullableString(createdBy),
	).Scan(&id)
	if err != nil {
		if pgIsUniqueViolation(err) {
			return apitypes.Monitor{}, errors.New("a monitor with this type+name already exists")
		}
		return apitypes.Monitor{}, fmt.Errorf("monitor insert: %w", err)
	}
	return s.GetMonitor(ctx, id)
}

func (s *Store) UpdateMonitor(ctx context.Context, id uuid.UUID, in apitypes.MonitorInput) (apitypes.Monitor, error) {
	if in.IntervalSec <= 0 {
		in.IntervalSec = 60
	}
	params, err := json.Marshal(orEmptyAny(in.Params))
	if err != nil {
		return apitypes.Monitor{}, err
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE monitors SET
			type = $2, name = $3, target = $4, params = $5,
			interval_sec = $6, enabled = $7
		WHERE id = $1`,
		id, in.Type, in.Name, in.Target, params, in.IntervalSec, in.Enabled)
	if err != nil {
		if pgIsUniqueViolation(err) {
			return apitypes.Monitor{}, errors.New("a monitor with this type+name already exists")
		}
		return apitypes.Monitor{}, fmt.Errorf("monitor update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apitypes.Monitor{}, ErrMonitorNotFound
	}
	return s.GetMonitor(ctx, id)
}

func (s *Store) DeleteMonitor(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM monitors WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrMonitorNotFound
	}
	return nil
}

// SaveMonitorResult inserts a sample and updates the latest-status columns
// on the monitors row in one transaction.
func (s *Store) SaveMonitorResult(ctx context.Context, id uuid.UUID, r probe.Result, at time.Time) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO monitor_results (time, monitor_id, status, latency_ms, detail)
		VALUES ($1, $2, $3, $4, $5)`,
		at, id, r.Status, r.LatencyMS, nullableString(r.Detail))
	if err != nil {
		return fmt.Errorf("monitor_results insert: %w", err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE monitors SET
			last_check_at   = $2,
			last_status     = $3,
			last_latency_ms = $4,
			last_detail     = $5
		WHERE id = $1`,
		id, at, r.Status, r.LatencyMS, truncate(r.Detail, 1000))
	if err != nil {
		return fmt.Errorf("monitor update: %w", err)
	}
	return tx.Commit(ctx)
}

// MonitorResults returns the most recent N results for a monitor.
func (s *Store) MonitorResults(ctx context.Context, id uuid.UUID, since time.Time, limit int) ([]apitypes.MonitorResult, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT time, status, COALESCE(latency_ms, 0), COALESCE(detail, '')
		FROM monitor_results
		WHERE monitor_id = $1 AND time >= $2
		ORDER BY time DESC
		LIMIT $3`, id, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.MonitorResult{}
	for rows.Next() {
		var r apitypes.MonitorResult
		if err := rows.Scan(&r.Time, &r.Status, &r.LatencyMS, &r.Detail); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanMonitor(scan func(...any) error) (apitypes.Monitor, error) {
	var (
		m            apitypes.Monitor
		idVal        uuid.UUID
		paramsRaw    []byte
		lastCheckAt  *time.Time
	)
	if err := scan(&idVal, &m.Type, &m.Name, &m.Target, &paramsRaw, &m.IntervalSec, &m.Enabled,
		&m.CreatedAt, &m.CreatedBy, &lastCheckAt, &m.LastStatus,
		&m.LastLatencyMS, &m.LastDetail); err != nil {
		return m, err
	}
	m.ID = idVal.String()
	m.Params = map[string]any{}
	if len(paramsRaw) > 0 {
		_ = json.Unmarshal(paramsRaw, &m.Params)
	}
	m.LastCheckAt = lastCheckAt
	// Redact DSN passwords from the API representation (still used internally
	// via GetMonitorRaw).
	m.Target = redactDSN(m.Target)
	return m, nil
}

// GetMonitorRaw returns the unredacted target. Internal only.
func (s *Store) GetMonitorRaw(ctx context.Context, id uuid.UUID) (apitypes.Monitor, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, type, name, target, params, interval_sec, enabled,
		       created_at, COALESCE(created_by,''),
		       last_check_at, COALESCE(last_status,''),
		       COALESCE(last_latency_ms,0), COALESCE(last_detail,'')
		FROM monitors WHERE id = $1`, id)
	if err != nil {
		return apitypes.Monitor{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return apitypes.Monitor{}, ErrMonitorNotFound
	}
	m, err := scanMonitor(rows.Scan)
	if err != nil {
		return m, err
	}
	// Re-fetch raw target unredacted.
	if err := s.Pool.QueryRow(ctx, `SELECT target FROM monitors WHERE id = $1`, id).Scan(&m.Target); err != nil {
		return m, err
	}
	return m, nil
}

// redactDSN strips the password from "scheme://user:pw@host/...".
func redactDSN(s string) string {
	scheme := indexOf(s, "://")
	if scheme < 0 {
		return s
	}
	rest := s[scheme+3:]
	at := indexOf(rest, "@")
	if at < 0 {
		return s
	}
	creds := rest[:at]
	if c := indexOf(creds, ":"); c >= 0 {
		creds = creds[:c+1] + "***"
	}
	return s[:scheme+3] + creds + rest[at:]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
