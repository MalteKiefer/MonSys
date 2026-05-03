package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

var ErrHostNotFound = errors.New("host not found")

func (s *Store) ListHosts(ctx context.Context) ([]apitypes.Host, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, hostname, COALESCE(distro,''), COALESCE(arch,''),
		       COALESCE(cpu_cores,0), COALESCE(ram_total_bytes,0),
		       COALESCE(agent_version,''), first_seen_at, last_seen_at, labels
		FROM hosts
		WHERE revoked_at IS NULL
		ORDER BY hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.Host{}
	for rows.Next() {
		var h apitypes.Host
		var labels []byte
		if err := rows.Scan(&h.ID, &h.Hostname, &h.Distro, &h.Arch,
			&h.CPUCores, &h.RAMTotalBytes, &h.AgentVersion,
			&h.FirstSeenAt, &h.LastSeenAt, &labels); err != nil {
			return nil, err
		}
		if len(labels) > 0 {
			_ = json.Unmarshal(labels, &h.Labels)
		}
		if h.Labels == nil {
			h.Labels = map[string]string{}
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) QuerySystemMetrics(ctx context.Context, hostID uuid.UUID, from, to time.Time) ([]apitypes.SystemSample, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hosts WHERE id = $1)`, hostID).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("host check: %w", err)
	}
	if !exists {
		return nil, ErrHostNotFound
	}

	rows, err := s.Pool.Query(ctx, `
		SELECT time, cpu_usage_pct, cpu_per_core,
		       load_1, load_5, load_15,
		       ram_used_bytes, ram_avail_bytes, swap_used_bytes, uptime_sec
		FROM metrics_system
		WHERE host_id = $1 AND time >= $2 AND time <= $3
		ORDER BY time ASC`,
		hostID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []apitypes.SystemSample{}
	for rows.Next() {
		var m apitypes.SystemSample
		var perCore []float64
		if err := rows.Scan(&m.Time, &m.CPUUsagePct, &perCore,
			&m.Load1, &m.Load5, &m.Load15,
			&m.RAMUsedBytes, &m.RAMAvailBytes, &m.SwapUsedBytes, &m.UptimeSec); err != nil {
			return nil, err
		}
		m.CPUPerCore = perCore
		out = append(out, m)
	}
	return out, rows.Err()
}

var _ = pgx.ErrNoRows
