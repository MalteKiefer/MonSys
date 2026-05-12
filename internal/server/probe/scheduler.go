package probe

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ResultEvent is the result fan-out target for the rules engine. Filled in by
// the scheduler so downstream consumers can subscribe without reaching into
// the DB.
type ResultEvent struct {
	MonitorID uuid.UUID
	Type      string
	Name      string
	Result    Result
	At        time.Time
}

// Scheduler runs every enabled monitor on its own ticker. It re-reads the
// monitors table every reloadInterval so newly created/edited monitors take
// effect without a server restart.
type Scheduler struct {
	Pool   *pgxpool.Pool
	Reload time.Duration
	Out    chan ResultEvent

	mu     sync.Mutex
	cancel map[uuid.UUID]context.CancelFunc
	known  map[uuid.UUID]int // monitor id → interval_sec at the time we started its loop
}

func NewScheduler(pool *pgxpool.Pool) *Scheduler {
	return &Scheduler{
		Pool:   pool,
		Reload: 30 * time.Second,
		Out:    make(chan ResultEvent, 256),
		cancel: map[uuid.UUID]context.CancelFunc{},
		known:  map[uuid.UUID]int{},
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.Reload)
	defer t.Stop()

	if err := s.reload(ctx); err != nil {
		slog.Warn("scheduler initial reload failed", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			s.stopAll()
			return
		case <-t.C:
			if err := s.reload(ctx); err != nil {
				slog.Warn("scheduler reload failed", "err", err)
			}
		}
	}
}

func (s *Scheduler) reload(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, type, name, target, params, interval_sec
		FROM monitors WHERE enabled = TRUE`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type row struct {
		id          uuid.UUID
		typ, name   string
		target      string
		paramsRaw   []byte
		intervalSec int
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.typ, &r.name, &r.target, &r.paramsRaw, &r.intervalSec); err != nil {
			return err
		}
		rs = append(rs, r)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop loops for monitors that vanished or had their interval changed.
	seen := map[uuid.UUID]struct{}{}
	for _, r := range rs {
		seen[r.id] = struct{}{}
		if iv, ok := s.known[r.id]; ok && iv == r.intervalSec {
			continue // unchanged
		}
		if cancel, ok := s.cancel[r.id]; ok {
			cancel()
		}
		params := map[string]any{}
		if len(r.paramsRaw) > 0 {
			_ = json.Unmarshal(r.paramsRaw, &params)
		}
		ctx2, cancel := context.WithCancel(ctx)
		s.cancel[r.id] = cancel
		s.known[r.id] = r.intervalSec
		go s.runLoop(ctx2, r.id, r.typ, r.name, r.target, params, time.Duration(r.intervalSec)*time.Second)
	}
	for id, cancel := range s.cancel {
		if _, ok := seen[id]; ok {
			continue
		}
		cancel()
		delete(s.cancel, id)
		delete(s.known, id)
	}
	return nil
}

func (s *Scheduler) runLoop(ctx context.Context, id uuid.UUID, typ, name, target string, params map[string]any, interval time.Duration) {
	// First check happens immediately so a freshly-created monitor doesn't
	// have to wait one interval for any signal at all.
	s.runOnce(ctx, id, typ, name, target, params)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.runOnce(ctx, id, typ, name, target, params)
		}
	}
}

func (s *Scheduler) runOnce(ctx context.Context, id uuid.UUID, typ, name, target string, params map[string]any) {
	probe := Probe{Type: typ, Target: target, Params: params}
	res := Run(ctx, probe)
	at := time.Now().UTC()

	if err := saveResult(ctx, s.Pool, id, res, at); err != nil {
		slog.Warn("monitor result save failed", "id", id, "err", err)
	}

	select {
	case s.Out <- ResultEvent{MonitorID: id, Type: typ, Name: name, Result: res, At: at}:
	default:
		// Dropping is preferable to blocking — the rules engine will catch
		// the next event, and the row is already in the DB.
	}
}

func (s *Scheduler) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, cancel := range s.cancel {
		cancel()
		delete(s.cancel, id)
		delete(s.known, id)
	}
}

// saveResult is split out so the store package can re-use the same query if
// it needs to record a result from elsewhere (test endpoints, manual runs).
func saveResult(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, r Result, at time.Time) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO monitor_results (time, monitor_id, status, latency_ms, detail)
		VALUES ($1, $2, $3, $4, $5)`,
		at, id, r.Status, r.LatencyMS, nullIfEmpty(r.Detail))
	if err != nil {
		return err
	}
	detail := r.Detail
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	_, err = tx.Exec(ctx, `
		UPDATE monitors SET
			last_check_at   = $2,
			last_status     = $3,
			last_latency_ms = $4,
			last_detail     = $5
		WHERE id = $1`,
		id, at, r.Status, r.LatencyMS, detail)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
