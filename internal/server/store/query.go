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
		SELECT h.id, h.hostname, COALESCE(h.distro,''), COALESCE(h.arch,''),
		       COALESCE(h.cpu_cores,0), COALESCE(h.ram_total_bytes,0),
		       COALESCE(h.agent_version,''), h.first_seen_at, h.last_seen_at, h.labels,
		       COALESCE(hs.status, 'unknown'),
		       hs.since
		FROM hosts h
		LEFT JOIN host_status hs ON hs.host_id = h.id
		WHERE h.revoked_at IS NULL
		ORDER BY h.hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.Host{}
	for rows.Next() {
		var (
			h           apitypes.Host
			labels      []byte
			statusSince *time.Time
		)
		if err := rows.Scan(&h.ID, &h.Hostname, &h.Distro, &h.Arch,
			&h.CPUCores, &h.RAMTotalBytes, &h.AgentVersion,
			&h.FirstSeenAt, &h.LastSeenAt, &labels,
			&h.Status, &statusSince); err != nil {
			return nil, err
		}
		if len(labels) > 0 {
			_ = json.Unmarshal(labels, &h.Labels)
		}
		if h.Labels == nil {
			h.Labels = map[string]string{}
		}
		if statusSince != nil {
			h.StatusSince = *statusSince
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

// HostSecurity bundles the security-relevant snapshot a UI typically wants
// in a single fetch.
type HostSecurity struct {
	Firewalls []apitypes.FirewallStatus   `json:"firewalls"`
	Fail2ban  []apitypes.Fail2banJailInfo `json:"fail2ban"`
	CrowdSec  []apitypes.CrowdsecDecision `json:"crowdsec"`
}

func (s *Store) HostSecurity(ctx context.Context, hostID uuid.UUID) (HostSecurity, error) {
	var hs HostSecurity

	rows, err := s.Pool.Query(ctx, `
		SELECT engine, active,
		       COALESCE(default_input,''), COALESCE(default_output,''), COALESCE(default_forward,''),
		       COALESCE(rule_count,0), COALESCE(snapshot_excerpt,'')
		FROM firewall_status WHERE host_id = $1`, hostID)
	if err != nil {
		return hs, fmt.Errorf("firewall_status: %w", err)
	}
	for rows.Next() {
		var f apitypes.FirewallStatus
		if err := rows.Scan(&f.Engine, &f.Active,
			&f.DefaultInput, &f.DefaultOutput, &f.DefaultForward,
			&f.RuleCount, &f.SnapshotExcerpt); err != nil {
			rows.Close()
			return hs, err
		}
		hs.Firewalls = append(hs.Firewalls, f)
	}
	rows.Close()

	rows, err = s.Pool.Query(ctx, `
		SELECT jail, COALESCE(currently_failed,0), COALESCE(total_failed,0),
		       COALESCE(currently_banned,0), COALESCE(total_banned,0), COALESCE(banned_ips,'{}')
		FROM fail2ban_jails WHERE host_id = $1`, hostID)
	if err != nil {
		return hs, fmt.Errorf("fail2ban_jails: %w", err)
	}
	for rows.Next() {
		var j apitypes.Fail2banJailInfo
		if err := rows.Scan(&j.Jail, &j.CurrentlyFailed, &j.TotalFailed,
			&j.CurrentlyBanned, &j.TotalBanned, &j.BannedIPs); err != nil {
			rows.Close()
			return hs, err
		}
		hs.Fail2ban = append(hs.Fail2ban, j)
	}
	rows.Close()

	rows, err = s.Pool.Query(ctx, `
		SELECT decision_id, COALESCE(origin,''), COALESCE(scope,''), COALESCE(target,''),
		       COALESCE(decision_type,''), COALESCE(reason,''), COALESCE(until, 'epoch'::timestamptz)
		FROM crowdsec_decisions WHERE host_id = $1`, hostID)
	if err != nil {
		return hs, fmt.Errorf("crowdsec_decisions: %w", err)
	}
	for rows.Next() {
		var d apitypes.CrowdsecDecision
		if err := rows.Scan(&d.DecisionID, &d.Origin, &d.Scope, &d.Target,
			&d.Type, &d.Reason, &d.Until); err != nil {
			rows.Close()
			return hs, err
		}
		hs.CrowdSec = append(hs.CrowdSec, d)
	}
	rows.Close()

	return hs, nil
}

func (s *Store) ListHostLogins(ctx context.Context, hostID uuid.UUID, since time.Time, limit int) ([]apitypes.LoginEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT time, COALESCE(username,''), COALESCE(source_ip,''),
		       COALESCE(method,''), success, COALESCE(detail,'')
		FROM login_events
		WHERE host_id = $1 AND time >= $2
		ORDER BY time DESC
		LIMIT $3`, hostID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.LoginEvent{}
	for rows.Next() {
		var e apitypes.LoginEvent
		if err := rows.Scan(&e.Time, &e.Username, &e.SourceIP, &e.Method, &e.Success, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

var _ = pgx.ErrNoRows
