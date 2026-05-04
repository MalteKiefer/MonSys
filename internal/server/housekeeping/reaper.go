// Package housekeeping bounds in-memory and database state that other
// components grow but never trim. It runs a single goroutine that ticks once
// an hour and:
//
//   - DELETEs from user_sessions where the session has expired or has been
//     revoked for more than 24h (revoked sessions are kept briefly so an
//     audit/debug query can still see them).
//   - DELETEs from user_action_tokens where the token is past expires_at
//     (covers invite, password_reset, login_2fa, totp_setup token kinds).
//   - Calls GC() on the failed-login tracker so the per-email buckets that
//     the in-memory rate limiter accumulates do not leak.
//
// Long-term, retention for hypertables is handled by Timescale retention
// policies (see migrations 0012 / 0016); this reaper only handles tables
// where a per-row TTL needs to be respected explicitly.
package housekeeping

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pr0ph37/mon/internal/server/store"
)

// defaultInterval is the period between reaper passes. One hour is a
// reasonable trade-off: short enough that a misconfigured invite or compromised
// session is purged within the day, long enough that the DELETEs are cheap.
const defaultInterval = 1 * time.Hour

// revokedSessionGrace controls how long revoked-but-not-yet-expired sessions
// stay around. 24h gives operators a window to inspect "who logged out and
// when" without keeping the table forever.
const revokedSessionGrace = 24 * time.Hour

// Reaper is the housekeeping worker. Construct via New and run via Run.
type Reaper struct {
	Pool     *pgxpool.Pool
	Tracker  *store.FailedLoginAttempts
	Interval time.Duration
}

// New builds a Reaper using the default tick interval.
func New(pool *pgxpool.Pool, tracker *store.FailedLoginAttempts) *Reaper {
	return &Reaper{
		Pool:     pool,
		Tracker:  tracker,
		Interval: defaultInterval,
	}
}

// Run blocks until ctx is cancelled, ticking every Interval and invoking
// Tick. A single Tick failure is logged and does not stop the loop.
func (r *Reaper) Run(ctx context.Context) {
	t := time.NewTicker(r.Interval)
	defer t.Stop()

	// Run once on startup so a freshly-restarted server reaps immediately
	// rather than waiting an hour.
	if err := r.Tick(ctx); err != nil {
		slog.Warn("housekeeping initial tick failed", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.Tick(ctx); err != nil {
				slog.Warn("housekeeping tick failed", "err", err)
			}
		}
	}
}

// Tick performs one reaper pass. Each step is independent: a failure of the
// session DELETE does not skip the action-token DELETE, and DB failures do
// not skip the in-memory GC.
func (r *Reaper) Tick(ctx context.Context) error {
	var firstErr error

	if r.Pool != nil {
		// Expired or long-revoked sessions. We pass the grace period in
		// seconds and let Postgres build the interval — `$1::interval` with a
		// Go string and `($1 || ' seconds')::interval` with an int both
		// misbehave under pgx (see MEMORY.md → pgx int||text concat).
		tag, err := r.Pool.Exec(ctx, `
			DELETE FROM user_sessions
			WHERE expires_at < now()
			   OR (revoked_at IS NOT NULL
			       AND revoked_at < now() - make_interval(secs => $1))`,
			int(revokedSessionGrace/time.Second))
		if err != nil {
			slog.Warn("housekeeping: user_sessions delete", "err", err)
			if firstErr == nil {
				firstErr = err
			}
		} else if n := tag.RowsAffected(); n > 0 {
			slog.Info("housekeeping: reaped user_sessions", "rows", n)
		}

		// Expired action tokens (invite, password reset, login_2fa, totp setup).
		tag, err = r.Pool.Exec(ctx,
			`DELETE FROM user_action_tokens WHERE expires_at < now()`)
		if err != nil {
			slog.Warn("housekeeping: user_action_tokens delete", "err", err)
			if firstErr == nil {
				firstErr = err
			}
		} else if n := tag.RowsAffected(); n > 0 {
			slog.Info("housekeeping: reaped user_action_tokens", "rows", n)
		}
	}

	// In-memory: drop stale failed-login buckets. Cannot fail.
	if r.Tracker != nil {
		r.Tracker.GC()
	}

	return firstErr
}
