package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// QueryDiskMetrics returns disk samples in [from, to], optionally filtered to
// a specific disk_id. Results are flat — caller groups by disk_id for charting.
func (s *Store) QueryDiskMetrics(ctx context.Context, hostID uuid.UUID, from, to time.Time, diskID *uuid.UUID) ([]apitypes.DiskSample, []string, error) {
	if err := s.requireHost(ctx, hostID); err != nil {
		return nil, nil, err
	}
	args := []any{hostID, from, to}
	filter := ""
	if diskID != nil {
		filter = " AND m.disk_id = $4"
		args = append(args, *diskID)
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT m.time, d.device, d.mountpoint,
		       COALESCE(m.used_bytes,0), COALESCE(m.free_bytes,0),
		       COALESCE(m.inodes_used,0), COALESCE(m.inodes_free,0),
		       COALESCE(m.read_bytes,0), COALESCE(m.write_bytes,0),
		       COALESCE(m.read_ops,0), COALESCE(m.write_ops,0),
		       COALESCE(m.io_time_ms,0)
		FROM metrics_disk m JOIN disks d ON d.id = m.disk_id
		WHERE m.host_id = $1 AND m.time >= $2 AND m.time <= $3`+filter+`
		ORDER BY m.time ASC`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []apitypes.DiskSample{}
	seen := map[string]struct{}{}
	devices := []string{}
	for rows.Next() {
		var s apitypes.DiskSample
		if err := rows.Scan(&s.Time, &s.Device, &s.Mountpoint,
			&s.UsedBytes, &s.FreeBytes, &s.InodesUsed, &s.InodesFree,
			&s.ReadBytes, &s.WriteBytes, &s.ReadOps, &s.WriteOps, &s.IOTimeMS); err != nil {
			return nil, nil, err
		}
		out = append(out, s)
		if _, ok := seen[s.Mountpoint]; !ok {
			seen[s.Mountpoint] = struct{}{}
			devices = append(devices, s.Mountpoint)
		}
	}
	return out, devices, rows.Err()
}

// QueryNetMetrics returns nic samples in [from, to].
func (s *Store) QueryNetMetrics(ctx context.Context, hostID uuid.UUID, from, to time.Time, nicID *uuid.UUID) ([]apitypes.NetSample, []string, error) {
	if err := s.requireHost(ctx, hostID); err != nil {
		return nil, nil, err
	}
	args := []any{hostID, from, to}
	filter := ""
	if nicID != nil {
		filter = " AND m.nic_id = $4"
		args = append(args, *nicID)
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT m.time, n.name,
		       COALESCE(m.rx_bytes,0), COALESCE(m.tx_bytes,0),
		       COALESCE(m.rx_pkts,0),  COALESCE(m.tx_pkts,0),
		       COALESCE(m.rx_errs,0),  COALESCE(m.tx_errs,0),
		       COALESCE(m.rx_drops,0), COALESCE(m.tx_drops,0)
		FROM metrics_net m JOIN nics n ON n.id = m.nic_id
		WHERE m.host_id = $1 AND m.time >= $2 AND m.time <= $3`+filter+`
		ORDER BY m.time ASC`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []apitypes.NetSample{}
	seen := map[string]struct{}{}
	nics := []string{}
	for rows.Next() {
		var s apitypes.NetSample
		if err := rows.Scan(&s.Time, &s.NicName,
			&s.RxBytes, &s.TxBytes, &s.RxPkts, &s.TxPkts,
			&s.RxErrs, &s.TxErrs, &s.RxDrops, &s.TxDrops); err != nil {
			return nil, nil, err
		}
		out = append(out, s)
		if _, ok := seen[s.NicName]; !ok {
			seen[s.NicName] = struct{}{}
			nics = append(nics, s.NicName)
		}
	}
	return out, nics, rows.Err()
}

func (s *Store) requireHost(ctx context.Context, hostID uuid.UUID) error {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hosts WHERE id = $1)`, hostID).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrHostNotFound
	}
	return nil
}

// SearchPackages searches installed packages across hosts (or one host) with
// optional manager filter and case-insensitive name/version matching.
type PackageSearchResult struct {
	HostID      string
	Hostname    string
	Manager     string
	Name        string
	Version     string
	Arch        string
	SourceRepo  string
	InstalledAt *time.Time
}

func (s *Store) SearchPackages(ctx context.Context, q, manager string, hostID *uuid.UUID, limit, offset int) ([]PackageSearchResult, int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	args := []any{}
	where := []string{"1=1"}
	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if q != "" {
		// ILIKE on name and version. Normalize the query so trailing/leading
		// whitespace doesn't matter.
		pattern := "%" + q + "%"
		where = append(where, fmt.Sprintf("(p.name ILIKE %s OR p.version ILIKE %s)", addArg(pattern), addArg(pattern)))
	}
	if manager != "" {
		where = append(where, fmt.Sprintf("p.manager = %s", addArg(manager)))
	}
	if hostID != nil {
		where = append(where, fmt.Sprintf("p.host_id = %s", addArg(*hostID)))
	}

	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM packages p WHERE `+whereSQL, args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := s.Pool.Query(ctx, `
		SELECT p.host_id, h.hostname, p.manager, p.name, p.version,
		       COALESCE(p.arch,''), COALESCE(p.source_repo,''), p.installed_at
		FROM packages p JOIN hosts h ON h.id = p.host_id
		WHERE `+whereSQL+`
		ORDER BY p.name, h.hostname
		LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []PackageSearchResult{}
	for rows.Next() {
		var r PackageSearchResult
		var hid uuid.UUID
		if err := rows.Scan(&hid, &r.Hostname, &r.Manager, &r.Name, &r.Version,
			&r.Arch, &r.SourceRepo, &r.InstalledAt); err != nil {
			return nil, 0, err
		}
		r.HostID = hid.String()
		out = append(out, r)
	}
	return out, total, rows.Err()
}

var _ = pgx.ErrNoRows
