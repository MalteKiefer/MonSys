package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// GetHostDetail returns a single host record + the latest inventory bundles
// (disks, nics, workloads, vms, users) and the most recent package summary.
// Time-series ranges and the security snapshot are fetched separately.
func (s *Store) GetHostDetail(ctx context.Context, id uuid.UUID) (apitypes.HostDetail, error) {
	var d apitypes.HostDetail

	// Host row.
	var (
		labels      []byte
		statusSince *time.Time
	)
	err := s.Pool.QueryRow(ctx, `
		SELECT h.id, h.hostname, COALESCE(h.distro,''), COALESCE(h.arch,''),
		       COALESCE(h.cpu_cores,0), COALESCE(h.ram_total_bytes,0),
		       COALESCE(h.agent_version,''), h.first_seen_at, h.last_seen_at, h.labels,
		       COALESCE(hs.status, 'unknown'),
		       hs.since
		FROM hosts h
		LEFT JOIN host_status hs ON hs.host_id = h.id
		WHERE h.id = $1`, id,
	).Scan(&d.Host.ID, &d.Host.Hostname, &d.Host.Distro, &d.Host.Arch,
		&d.Host.CPUCores, &d.Host.RAMTotalBytes, &d.Host.AgentVersion,
		&d.Host.FirstSeenAt, &d.Host.LastSeenAt, &labels,
		&d.Host.Status, &statusSince)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrHostNotFound
	}
	if err != nil {
		return d, err
	}
	if len(labels) > 0 {
		_ = json.Unmarshal(labels, &d.Host.Labels)
	}
	if d.Host.Labels == nil {
		d.Host.Labels = map[string]string{}
	}
	if statusSince != nil {
		d.Host.StatusSince = *statusSince
	}

	if d.Disks, err = s.hostDisks(ctx, id); err != nil {
		return d, fmt.Errorf("disks: %w", err)
	}
	if d.Nics, err = s.hostNics(ctx, id); err != nil {
		return d, fmt.Errorf("nics: %w", err)
	}
	if d.Workloads, err = s.hostWorkloads(ctx, id); err != nil {
		return d, fmt.Errorf("workloads: %w", err)
	}
	if d.VMs, err = s.hostVMs(ctx, id); err != nil {
		return d, fmt.Errorf("vms: %w", err)
	}
	if d.Users, err = s.hostUsers(ctx, id); err != nil {
		return d, fmt.Errorf("users: %w", err)
	}
	if d.PackagesSummary, err = s.hostPackageSummary(ctx, id); err != nil {
		return d, fmt.Errorf("package summary: %w", err)
	}
	if d.RepoStates, err = s.hostRepoStates(ctx, id); err != nil {
		return d, fmt.Errorf("repo states: %w", err)
	}

	// Tags + groups + derived fields populate the embedded Host.
	tags, gerr := s.hostTags(ctx, id)
	if gerr == nil {
		d.Host.Tags = tags
	}
	groups, gerr := s.hostGroups(ctx, id)
	if gerr == nil {
		d.Host.Groups = groups
	}
	d.Host.DistroFamily = distroFamily(d.Host.Distro)
	if svc, serr := s.detectServices(ctx, []uuid.UUID{id}); serr == nil {
		d.Host.Services = svc[id.String()]
	}
	return d, nil
}

func (s *Store) hostTags(ctx context.Context, hostID uuid.UUID) ([]string, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT tag FROM host_tags WHERE host_id = $1 ORDER BY tag`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) hostGroups(ctx context.Context, hostID uuid.UUID) ([]apitypes.HostGroupRef, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT g.id, g.name FROM host_group_members m
		JOIN host_groups g ON g.id = m.group_id
		WHERE m.host_id = $1 ORDER BY g.name`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.HostGroupRef{}
	for rows.Next() {
		var r apitypes.HostGroupRef
		var id uuid.UUID
		if err := rows.Scan(&id, &r.Name); err != nil {
			return nil, err
		}
		r.ID = id.String()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) hostDisks(ctx context.Context, id uuid.UUID) ([]apitypes.DiskRow, error) {
	// Pick the most recent metrics_disk per disk via DISTINCT ON.
	rows, err := s.Pool.Query(ctx, `
		SELECT d.id, d.device, d.mountpoint, COALESCE(d.fstype,''),
		       COALESCE(d.size_bytes,0), COALESCE(d.is_removable, false), d.last_seen_at,
		       m.time, COALESCE(m.used_bytes,0), COALESCE(m.free_bytes,0)
		FROM disks d
		LEFT JOIN LATERAL (
			SELECT time, used_bytes, free_bytes
			FROM metrics_disk
			WHERE host_id = d.host_id AND disk_id = d.id
			ORDER BY time DESC LIMIT 1
		) m ON TRUE
		WHERE d.host_id = $1
		ORDER BY d.mountpoint`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.DiskRow{}
	for rows.Next() {
		var r apitypes.DiskRow
		var idVal uuid.UUID
		if err := rows.Scan(&idVal, &r.Device, &r.Mountpoint, &r.FSType,
			&r.SizeBytes, &r.IsRemovable, &r.LastSeenAt,
			&r.LatestTime, &r.UsedBytes, &r.FreeBytes); err != nil {
			return nil, err
		}
		r.ID = idVal.String()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) hostNics(ctx context.Context, id uuid.UUID) ([]apitypes.NicRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT n.id, n.name, COALESCE(n.mac,''), COALESCE(n.speed_mbps,0), n.last_seen_at,
		       m.time, COALESCE(m.rx_bytes,0), COALESCE(m.tx_bytes,0)
		FROM nics n
		LEFT JOIN LATERAL (
			SELECT time, rx_bytes, tx_bytes
			FROM metrics_net
			WHERE host_id = n.host_id AND nic_id = n.id
			ORDER BY time DESC LIMIT 1
		) m ON TRUE
		WHERE n.host_id = $1
		ORDER BY n.name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.NicRow{}
	for rows.Next() {
		var r apitypes.NicRow
		var idVal uuid.UUID
		if err := rows.Scan(&idVal, &r.Name, &r.MAC, &r.SpeedMbps, &r.LastSeenAt,
			&r.LatestTime, &r.RxBytes, &r.TxBytes); err != nil {
			return nil, err
		}
		r.ID = idVal.String()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) hostWorkloads(ctx context.Context, id uuid.UUID) ([]apitypes.WorkloadRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT w.id, w.kind, w.external_id, COALESCE(w.name,''), COALESCE(w.image,''),
		       COALESCE(w.state,''), w.labels, w.last_seen_at,
		       m.time, COALESCE(m.cpu_usage_pct,0), COALESCE(m.mem_used_bytes,0)
		FROM workloads w
		LEFT JOIN LATERAL (
			SELECT time, cpu_usage_pct, mem_used_bytes
			FROM metrics_workload
			WHERE host_id = w.host_id AND workload_id = w.id
			ORDER BY time DESC LIMIT 1
		) m ON TRUE
		WHERE w.host_id = $1
		ORDER BY w.name, w.external_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.WorkloadRow{}
	for rows.Next() {
		var r apitypes.WorkloadRow
		var idVal uuid.UUID
		var labelsRaw []byte
		if err := rows.Scan(&idVal, &r.Kind, &r.ExternalID, &r.Name, &r.Image,
			&r.State, &labelsRaw, &r.LastSeenAt,
			&r.LatestTime, &r.CPUUsagePct, &r.MemUsedBytes); err != nil {
			return nil, err
		}
		r.ID = idVal.String()
		r.Labels = map[string]string{}
		if len(labelsRaw) > 0 {
			_ = json.Unmarshal(labelsRaw, &r.Labels)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) hostVMs(ctx context.Context, id uuid.UUID) ([]apitypes.VMRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT kind, external_id, COALESCE(name,''), COALESCE(state,''),
		       COALESCE(vcpu,0), COALESCE(mem_bytes,0), COALESCE(autostart,false),
		       last_seen_at
		FROM vms WHERE host_id = $1
		ORDER BY kind, name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.VMRow{}
	for rows.Next() {
		var r apitypes.VMRow
		if err := rows.Scan(&r.Kind, &r.ExternalID, &r.Name, &r.State,
			&r.VCPU, &r.MemBytes, &r.Autostart, &r.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) hostUsers(ctx context.Context, id uuid.UUID) ([]apitypes.ObservedUser, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT username, COALESCE(uid,0), COALESCE(gid,0),
		       COALESCE(shell,''), COALESCE(home,''),
		       is_sudoer, is_system, last_login_at, last_seen_at
		FROM observed_users WHERE host_id = $1
		ORDER BY uid, username`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.ObservedUser{}
	for rows.Next() {
		var u apitypes.ObservedUser
		if err := rows.Scan(&u.Username, &u.UID, &u.GID, &u.Shell, &u.Home,
			&u.IsSudoer, &u.IsSystem, &u.LastLoginAt, &u.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) hostPackageSummary(ctx context.Context, id uuid.UUID) (*apitypes.PackageSummaryRow, error) {
	var r apitypes.PackageSummaryRow
	err := s.Pool.QueryRow(ctx, `
		SELECT time, COALESCE(installed_count,0), COALESCE(updates_count,0),
		       COALESCE(security_updates,0), COALESCE(metadata_age_sec,0)
		FROM metrics_packages_summary
		WHERE host_id = $1
		ORDER BY time DESC LIMIT 1`, id,
	).Scan(&r.Time, &r.InstalledCount, &r.UpdatesCount, &r.SecurityUpdates, &r.MetadataAgeSec)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) hostRepoStates(ctx context.Context, id uuid.UUID) ([]apitypes.RepoMetaState, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT manager, COALESCE(metadata_mtime, 'epoch'::timestamptz),
		       COALESCE(metadata_age_seconds,0), refreshed_externally
		FROM package_repo_state WHERE host_id = $1
		ORDER BY manager`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.RepoMetaState{}
	for rows.Next() {
		var r apitypes.RepoMetaState
		if err := rows.Scan(&r.Manager, &r.MetadataMtime, &r.MetadataAgeSec, &r.RefreshedExternally); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListHostPackages paginates the installed packages for a host.
func (s *Store) ListHostPackages(ctx context.Context, id uuid.UUID, limit, offset int) ([]apitypes.PackageRow, int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM packages WHERE host_id = $1`, id,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT manager, name, version, COALESCE(arch,''),
		       COALESCE(source_repo,''), installed_at
		FROM packages WHERE host_id = $1
		ORDER BY manager, name
		LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []apitypes.PackageRow{}
	for rows.Next() {
		var p apitypes.PackageRow
		if err := rows.Scan(&p.Manager, &p.Name, &p.Version, &p.Arch,
			&p.SourceRepo, &p.InstalledAt); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// ListHostPackageUpdates returns the pending updates for a host.
func (s *Store) ListHostPackageUpdates(ctx context.Context, id uuid.UUID) ([]apitypes.PendingUpdate, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT manager, name, COALESCE(arch,''), current_version, available_version,
		       COALESCE(source_repo,''), is_security
		FROM package_updates WHERE host_id = $1
		ORDER BY manager, name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.PendingUpdate{}
	for rows.Next() {
		var p apitypes.PendingUpdate
		if err := rows.Scan(&p.Manager, &p.Name, &p.Arch, &p.CurrentVersion,
			&p.AvailableVersion, &p.SourceRepo, &p.IsSecurity); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
