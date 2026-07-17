package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// MailQueuePoint is a single time-series sample of the Postfix queue depth.
type MailQueuePoint struct {
	Time     time.Time
	Total    int
	Deferred int
}

// SaveMailReport persists a mail report for the given host. It upserts
// host_mail_status (replacing the existing JSONB snapshot) and inserts a row
// into the metrics_mail hypertable for trending.
func (s *Store) SaveMailReport(ctx context.Context, hostID uuid.UUID, r apitypes.MailReport) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}

	_, err = s.Pool.Exec(ctx, `
		INSERT INTO host_mail_status (host_id, updated_at, report)
		VALUES ($1, now(), $2)
		ON CONFLICT (host_id) DO UPDATE
		  SET updated_at = now(),
		      report     = EXCLUDED.report`,
		hostID, raw)
	if err != nil {
		return err
	}

	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}

	var (
		qActive, qDeferred, qHold, qTotal int
		rspamdGreylisted, rspamdRejected   int64
	)
	if r.Queue != nil {
		qActive = r.Queue.Active
		qDeferred = r.Queue.Deferred
		qHold = r.Queue.Hold
		qTotal = r.Queue.Total
	}
	if r.Rspamd != nil {
		rspamdGreylisted = r.Rspamd.Greylisted
		rspamdRejected = r.Rspamd.Rejected
	}

	_, err = s.Pool.Exec(ctx, `
		INSERT INTO metrics_mail
		  (time, host_id, queue_active, queue_deferred, queue_hold, queue_total,
		   rspamd_greylisted, rspamd_rejected)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		t, hostID,
		qActive, qDeferred, qHold, qTotal,
		rspamdGreylisted, rspamdRejected)
	return err
}

// MailStatus returns the latest mail report for a host. If no report has been
// saved yet, it returns (zero, false, nil).
func (s *Store) MailStatus(ctx context.Context, hostID uuid.UUID) (apitypes.MailReport, bool, error) {
	var raw []byte
	err := s.Pool.QueryRow(ctx,
		`SELECT report FROM host_mail_status WHERE host_id = $1`, hostID,
	).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return apitypes.MailReport{}, false, nil
		}
		return apitypes.MailReport{}, false, err
	}

	var report apitypes.MailReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return apitypes.MailReport{}, false, err
	}
	return report, true, nil
}

// MailQueueSeries returns time-series queue depth samples for a host since the
// given time, ordered chronologically.
func (s *Store) MailQueueSeries(ctx context.Context, hostID uuid.UUID, since time.Time) ([]MailQueuePoint, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT time, queue_total, queue_deferred
		FROM metrics_mail
		WHERE host_id = $1 AND time >= $2
		ORDER BY time`,
		hostID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MailQueuePoint
	for rows.Next() {
		var p MailQueuePoint
		if err := rows.Scan(&p.Time, &p.Total, &p.Deferred); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
