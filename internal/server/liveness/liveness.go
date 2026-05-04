// Package liveness derives host status (online/stale/offline) from the
// last_seen_at column on the hosts table and persists transitions to
// host_status / host_status_history.
//
// The agent ticks every cfg.IntervalSeconds (default 15s) and updates
// last_seen_at on each ingest. We pick conservative thresholds that work for
// a 15s tick: stale at 90s (≈6 missed ticks), offline at 5m (≈20 missed).
// Operators with longer intervals can override via env vars at startup.
package liveness

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	StatusOnline  = "online"
	StatusStale   = "stale"
	StatusOffline = "offline"
)

// Thresholds defines when a host transitions out of "online".
type Thresholds struct {
	Stale   time.Duration // last_seen older than this → stale
	Offline time.Duration // last_seen older than this → offline
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		Stale:   durFromEnv("MON_LIVENESS_STALE", 90*time.Second),
		Offline: durFromEnv("MON_LIVENESS_OFFLINE", 5*time.Minute),
	}
}

func durFromEnv(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	return def
}

// Transition is emitted when a host changes status, used by the notification
// rules engine.
type Transition struct {
	HostID   uuid.UUID
	Hostname string
	From     string
	To       string
	At       time.Time
}

// Watcher periodically scans hosts and updates host_status. On each
// transition it sends a Transition on the Out channel (non-blocking — drops
// when the consumer is slow rather than stalling the watcher).
type Watcher struct {
	Pool       *pgxpool.Pool
	Thresholds Thresholds
	Interval   time.Duration
	Out        chan Transition
}

func New(pool *pgxpool.Pool) *Watcher {
	return &Watcher{
		Pool:       pool,
		Thresholds: DefaultThresholds(),
		Interval:   30 * time.Second,
		Out:        make(chan Transition, 64),
	}
}

func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.Interval)
	defer t.Stop()

	// Run once immediately on start so a fresh server has accurate status.
	if err := w.Tick(ctx); err != nil {
		slog.Warn("liveness initial tick failed", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.Tick(ctx); err != nil {
				slog.Warn("liveness tick failed", "err", err)
			}
		}
	}
}

// Tick reads every host's last_seen_at, computes the derived status, and
// upserts host_status. Transitions are recorded in host_status_history and
// fanned out on Out.
func (w *Watcher) Tick(ctx context.Context) error {
	rows, err := w.Pool.Query(ctx, `
		SELECT h.id, h.hostname, h.last_seen_at,
		       hs.status
		FROM hosts h
		LEFT JOIN host_status hs ON hs.host_id = h.id
		WHERE h.revoked_at IS NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()

	now := time.Now().UTC()

	type row struct {
		id       uuid.UUID
		hostname string
		lastSeen *time.Time
		current  *string
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.hostname, &r.lastSeen, &r.current); err != nil {
			return err
		}
		rs = append(rs, r)
	}

	for _, r := range rs {
		// Freshly registered hosts have last_seen_at = NULL until their first
		// ingest. Without this guard we'd compute now.Sub(zero-time) and fire
		// host_offline immediately on registration. Skip until we have data.
		if r.lastSeen == nil {
			continue
		}
		want := classify(now.Sub(r.lastSeen.UTC()), w.Thresholds)
		have := ""
		if r.current != nil {
			have = *r.current
		}
		if have == want {
			// Still bump last_check_at so operators can see liveness running.
			_, _ = w.Pool.Exec(ctx,
				`UPDATE host_status SET last_check_at = $2 WHERE host_id = $1`,
				r.id, now)
			continue
		}
		if err := w.applyTransition(ctx, r.id, r.hostname, have, want, now); err != nil {
			slog.Warn("liveness transition failed",
				"host_id", r.id, "from", have, "to", want, "err", err)
		}
	}
	return nil
}

func (w *Watcher) applyTransition(ctx context.Context, hostID uuid.UUID, hostname, from, to string, at time.Time) error {
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO host_status (host_id, status, since, last_check_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (host_id) DO UPDATE SET
			status        = EXCLUDED.status,
			since         = EXCLUDED.since,
			last_check_at = EXCLUDED.last_check_at`,
		hostID, to, at)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO host_status_history (host_id, from_status, to_status, at) VALUES ($1, NULLIF($2,''), $3, $4)`,
		hostID, from, to, at)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// Non-blocking publish.
	select {
	case w.Out <- Transition{HostID: hostID, Hostname: hostname, From: from, To: to, At: at}:
	default:
		slog.Warn("liveness Out channel full; dropping transition",
			"host_id", hostID, "from", from, "to", to)
	}
	return nil
}

func classify(age time.Duration, t Thresholds) string {
	switch {
	case age >= t.Offline:
		return StatusOffline
	case age >= t.Stale:
		return StatusStale
	default:
		return StatusOnline
	}
}
