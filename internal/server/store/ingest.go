package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// SaveIngest persists an IngestRequest for the given host: optional inventory
// upsert first (so disk/nic/workload IDs exist), then metric samples.
func (s *Store) SaveIngest(ctx context.Context, hostID uuid.UUID, req apitypes.IngestRequest) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if req.Inventory != nil {
		if err := saveInventoryTx(ctx, tx, hostID, *req.Inventory); err != nil {
			return fmt.Errorf("inventory: %w", err)
		}
		if err := saveVMsTx(ctx, tx, hostID, req.Inventory.VMs); err != nil {
			return fmt.Errorf("vms: %w", err)
		}
		if err := saveUsersTx(ctx, tx, hostID, req.Inventory.Users); err != nil {
			return fmt.Errorf("users: %w", err)
		}
	}

	if len(req.Logins) > 0 {
		if err := saveLoginEventsTx(ctx, tx, hostID, req.Logins); err != nil {
			return fmt.Errorf("logins: %w", err)
		}
	}
	if req.Security != nil {
		if err := saveSecurityTx(ctx, tx, hostID, *req.Security); err != nil {
			return fmt.Errorf("security: %w", err)
		}
	}

	disks, err := loadDiskIDs(ctx, tx, hostID)
	if err != nil {
		return fmt.Errorf("load disk ids: %w", err)
	}
	nics, err := loadNicIDs(ctx, tx, hostID)
	if err != nil {
		return fmt.Errorf("load nic ids: %w", err)
	}
	workloads, err := loadWorkloadIDs(ctx, tx, hostID)
	if err != nil {
		return fmt.Errorf("load workload ids: %w", err)
	}

	batch := &pgx.Batch{}

	for _, m := range req.System {
		batch.Queue(`
			INSERT INTO metrics_system (
				time, host_id, cpu_usage_pct, cpu_per_core,
				load_1, load_5, load_15,
				ram_used_bytes, ram_avail_bytes, swap_used_bytes, uptime_sec
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			m.Time, hostID, m.CPUUsagePct, m.CPUPerCore,
			m.Load1, m.Load5, m.Load15,
			m.RAMUsedBytes, m.RAMAvailBytes, m.SwapUsedBytes, m.UptimeSec)
	}

	for _, d := range req.Disks {
		id, ok := disks[diskKey{d.Device, d.Mountpoint}]
		if !ok {
			continue
		}
		batch.Queue(`
			INSERT INTO metrics_disk (
				time, host_id, disk_id, used_bytes, free_bytes,
				inodes_used, inodes_free, read_bytes, write_bytes,
				read_ops, write_ops, io_time_ms
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			d.Time, hostID, id, d.UsedBytes, d.FreeBytes,
			d.InodesUsed, d.InodesFree, d.ReadBytes, d.WriteBytes,
			d.ReadOps, d.WriteOps, d.IOTimeMS)
	}

	for _, n := range req.Nics {
		id, ok := nics[n.NicName]
		if !ok {
			continue
		}
		batch.Queue(`
			INSERT INTO metrics_net (
				time, host_id, nic_id,
				rx_bytes, tx_bytes, rx_pkts, tx_pkts,
				rx_errs, tx_errs, rx_drops, tx_drops
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			n.Time, hostID, id,
			n.RxBytes, n.TxBytes, n.RxPkts, n.TxPkts,
			n.RxErrs, n.TxErrs, n.RxDrops, n.TxDrops)
	}

	for _, w := range req.Workloads {
		id, ok := workloads[workloadKey{w.Kind, w.ExternalID}]
		if !ok {
			continue
		}
		batch.Queue(`
			INSERT INTO metrics_workload (
				time, host_id, workload_id,
				cpu_usage_pct, mem_used_bytes, mem_limit_bytes,
				net_rx_bytes, net_tx_bytes, block_read_bytes, block_write_bytes, state
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			w.Time, hostID, id,
			w.CPUUsagePct, w.MemUsedBytes, w.MemLimitBytes,
			w.NetRxBytes, w.NetTxBytes, w.BlockReadBytes, w.BlockWriteBytes, w.State)
	}

	if req.Packages != nil {
		p := req.Packages
		batch.Queue(`
			INSERT INTO metrics_packages_summary (
				time, host_id, installed_count, updates_count, security_updates, metadata_age_sec
			) VALUES ($1,$2,$3,$4,$5,$6)`,
			p.Time, hostID, p.Summary.InstalledCount, p.Summary.UpdatesCount,
			p.Summary.SecurityUpdates, p.Summary.MetadataAgeSec)

		if err := savePackagesTx(ctx, tx, hostID, p); err != nil {
			return fmt.Errorf("packages: %w", err)
		}
	}

	if batch.Len() > 0 {
		br := tx.SendBatch(ctx, batch)
		for i := 0; i < batch.Len(); i++ {
			if _, err := br.Exec(); err != nil {
				_ = br.Close()
				return fmt.Errorf("batch exec[%d]: %w", i, err)
			}
		}
		if err := br.Close(); err != nil {
			return fmt.Errorf("batch close: %w", err)
		}
	}

	return tx.Commit(ctx)
}

type (
	diskKey     struct{ Device, Mountpoint string }
	workloadKey struct{ Kind, ExternalID string }
)

func saveInventoryTx(ctx context.Context, tx pgx.Tx, hostID uuid.UUID, snap apitypes.InventorySnap) error {
	if snap.Hostname != "" || snap.Kernel != "" || snap.Distro != "" || snap.AgentVersion != "" || len(snap.Labels) > 0 {
		labelsJSON, err := json.Marshal(orEmpty(snap.Labels))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE hosts SET
				hostname        = COALESCE(NULLIF($2,''), hostname),
				kernel          = COALESCE(NULLIF($3,''), kernel),
				distro          = COALESCE(NULLIF($4,''), distro),
				cpu_model       = COALESCE(NULLIF($5,''), cpu_model),
				cpu_cores       = COALESCE(NULLIF($6,0), cpu_cores),
				ram_total_bytes = COALESCE(NULLIF($7::bigint,0), ram_total_bytes),
				agent_version   = COALESCE(NULLIF($8,''), agent_version),
				labels          = CASE WHEN $9::jsonb = '{}'::jsonb THEN labels ELSE $9::jsonb END,
				last_seen_at    = now()
			WHERE id = $1`,
			hostID, snap.Hostname, snap.Kernel, snap.Distro,
			snap.CPUModel, snap.CPUCores, snap.RAMTotalBytes,
			snap.AgentVersion, labelsJSON)
		if err != nil {
			return fmt.Errorf("hosts update: %w", err)
		}
	}

	for _, d := range snap.Disks {
		if d.Device == "" || d.Mountpoint == "" {
			continue
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO disks (host_id, device, mountpoint, fstype, size_bytes, is_removable)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (host_id, device, mountpoint) DO UPDATE SET
				fstype       = EXCLUDED.fstype,
				size_bytes   = EXCLUDED.size_bytes,
				is_removable = EXCLUDED.is_removable,
				last_seen_at = now()`,
			hostID, d.Device, d.Mountpoint, d.FSType, d.SizeBytes, d.IsRemovable)
		if err != nil {
			return fmt.Errorf("disks upsert: %w", err)
		}
	}

	for _, n := range snap.Nics {
		if n.Name == "" {
			continue
		}
		addrs := n.Addrs
		if addrs == nil {
			addrs = []string{}
		}
		// members defaults to []. bridge_master is nullable so partial
		// reports (older agents that don't populate it) don't blow away a
		// previously-known value — see 0028_nic_members.sql.
		members := n.Members
		if members == nil {
			members = []string{}
		}
		master := nullableString(n.BridgeMaster)
		_, err := tx.Exec(ctx, `
			INSERT INTO nics (host_id, name, mac, speed_mbps, addrs, members, bridge_master)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (host_id, name) DO UPDATE SET
				mac           = EXCLUDED.mac,
				speed_mbps    = EXCLUDED.speed_mbps,
				addrs         = EXCLUDED.addrs,
				members       = EXCLUDED.members,
				bridge_master = COALESCE(EXCLUDED.bridge_master, nics.bridge_master),
				last_seen_at  = now()`,
			hostID, n.Name, n.MAC, n.SpeedMbps, addrs, members, master)
		if err != nil {
			return fmt.Errorf("nics upsert: %w", err)
		}
	}

	for _, w := range snap.Workloads {
		if w.Kind == "" || w.ExternalID == "" {
			continue
		}
		labelsJSON, err := json.Marshal(orEmpty(w.Labels))
		if err != nil {
			return err
		}
		// Image-update detection (see migration 0027): the agent reports
		// current_digest (from `docker inspect`) and latest_digest (HEAD on
		// the upstream registry's manifest). Empty strings are persisted as
		// NULLs so partial reports (e.g. registry unreachable on this tick)
		// don't clobber a previously-known good digest. update_checked_at
		// is only stamped when the agent had something meaningful to say —
		// i.e. either side of the comparison is populated.
		curDigest := nullableString(w.CurrentDigest)
		latDigest := nullableString(w.LatestDigest)
		var checkedAt any
		if w.CurrentDigest != "" || w.LatestDigest != "" {
			checkedAt = time.Now().UTC()
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO workloads (
				host_id, kind, external_id, name, image, state, labels,
				current_digest, latest_digest, update_available, update_checked_at
			)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (host_id, kind, external_id) DO UPDATE SET
				name              = EXCLUDED.name,
				image             = EXCLUDED.image,
				state             = EXCLUDED.state,
				labels            = EXCLUDED.labels,
				current_digest    = COALESCE(EXCLUDED.current_digest,    workloads.current_digest),
				latest_digest     = COALESCE(EXCLUDED.latest_digest,     workloads.latest_digest),
				update_available  = EXCLUDED.update_available,
				update_checked_at = COALESCE(EXCLUDED.update_checked_at, workloads.update_checked_at),
				last_seen_at      = now()`,
			hostID, w.Kind, w.ExternalID, w.Name, w.Image, w.State, labelsJSON,
			curDigest, latDigest, w.UpdateAvailable, checkedAt)
		if err != nil {
			return fmt.Errorf("workloads upsert: %w", err)
		}
	}

	return nil
}

func loadDiskIDs(ctx context.Context, tx pgx.Tx, hostID uuid.UUID) (map[diskKey]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT id, device, mountpoint FROM disks WHERE host_id = $1`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[diskKey]uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		var dev, mp string
		if err := rows.Scan(&id, &dev, &mp); err != nil {
			return nil, err
		}
		out[diskKey{dev, mp}] = id
	}
	return out, rows.Err()
}

func loadNicIDs(ctx context.Context, tx pgx.Tx, hostID uuid.UUID) (map[string]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT id, name FROM nics WHERE host_id = $1`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
}

func loadWorkloadIDs(ctx context.Context, tx pgx.Tx, hostID uuid.UUID) (map[workloadKey]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT id, kind, external_id FROM workloads WHERE host_id = $1`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[workloadKey]uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		var kind, ext string
		if err := rows.Scan(&id, &kind, &ext); err != nil {
			return nil, err
		}
		out[workloadKey{kind, ext}] = id
	}
	return out, rows.Err()
}

// savePackagesTx upserts the installed-package list (only when the agent
// actually re-sent it, signalled by a non-empty Installed slice), refreshes
// pending-update rows, and updates per-manager repo metadata. The full
// installed list is replaced wholesale: we delete rows that were last seen
// before this batch's timestamp, so removed packages disappear without us
// needing the agent to remember the diff.
func savePackagesTx(ctx context.Context, tx pgx.Tx, hostID uuid.UUID, p *apitypes.PackageReport) error {
	if len(p.Installed) > 0 {
		seenAt := p.Time.UTC()
		for _, pkg := range p.Installed {
			_, err := tx.Exec(ctx, `
				INSERT INTO packages (
					host_id, manager, name, version, arch, source_repo, installed_at, last_seen_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
				ON CONFLICT (host_id, manager, name, arch) DO UPDATE SET
					version       = EXCLUDED.version,
					source_repo   = EXCLUDED.source_repo,
					installed_at  = COALESCE(EXCLUDED.installed_at, packages.installed_at),
					last_seen_at  = EXCLUDED.last_seen_at`,
				hostID, pkg.Manager, pkg.Name, pkg.Version, pkg.Arch,
				nullableString(pkg.SourceRepo), pkg.InstalledAt, seenAt)
			if err != nil {
				return fmt.Errorf("packages upsert: %w", err)
			}
		}
		// Drop rows we didn't see in this snapshot for the same managers.
		managers := uniqueManagers(p.Installed)
		if len(managers) > 0 {
			if _, err := tx.Exec(ctx, `
				DELETE FROM packages
				WHERE host_id = $1
				  AND manager = ANY($2)
				  AND last_seen_at < $3`,
				hostID, managers, seenAt); err != nil {
				return fmt.Errorf("packages prune: %w", err)
			}
		}
	}

	// Pending updates are always replaced for the managers we report on.
	if len(p.Updates) > 0 {
		managers := uniqueUpdateManagers(p.Updates)
		if _, err := tx.Exec(ctx,
			`DELETE FROM package_updates WHERE host_id = $1 AND manager = ANY($2)`,
			hostID, managers); err != nil {
			return fmt.Errorf("package_updates prune: %w", err)
		}
		for _, u := range p.Updates {
			_, err := tx.Exec(ctx, `
				INSERT INTO package_updates (
					host_id, manager, name, arch, current_version, available_version,
					source_repo, is_security
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
				ON CONFLICT (host_id, manager, name, arch) DO UPDATE SET
					current_version   = EXCLUDED.current_version,
					available_version = EXCLUDED.available_version,
					source_repo       = EXCLUDED.source_repo,
					is_security       = EXCLUDED.is_security,
					detected_at       = now()`,
				hostID, u.Manager, u.Name, u.Arch, u.CurrentVersion, u.AvailableVersion,
				nullableString(u.SourceRepo), u.IsSecurity)
			if err != nil {
				return fmt.Errorf("package_updates upsert: %w", err)
			}
		}
	}

	for _, rs := range p.RepoStates {
		_, err := tx.Exec(ctx, `
			INSERT INTO package_repo_state (
				host_id, manager, metadata_mtime, metadata_age_seconds, refreshed_externally, updated_at
			) VALUES ($1,$2,$3,$4,$5, now())
			ON CONFLICT (host_id, manager) DO UPDATE SET
				metadata_mtime       = EXCLUDED.metadata_mtime,
				metadata_age_seconds = EXCLUDED.metadata_age_seconds,
				refreshed_externally = EXCLUDED.refreshed_externally,
				updated_at           = now()`,
			hostID, rs.Manager, rs.MetadataMtime, rs.MetadataAgeSec, rs.RefreshedExternally)
		if err != nil {
			return fmt.Errorf("package_repo_state upsert: %w", err)
		}
	}
	return nil
}

func saveVMsTx(ctx context.Context, tx pgx.Tx, hostID uuid.UUID, vms []apitypes.VMInfo) error {
	if len(vms) == 0 {
		return nil
	}
	seenAt := time.Now().UTC()
	kinds := map[string]struct{}{}
	for _, v := range vms {
		if v.Kind == "" || v.ExternalID == "" {
			continue
		}
		kinds[v.Kind] = struct{}{}
		_, err := tx.Exec(ctx, `
			INSERT INTO vms (host_id, kind, external_id, name, state, vcpu, mem_bytes, autostart, last_seen_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (host_id, kind, external_id) DO UPDATE SET
				name         = EXCLUDED.name,
				state        = EXCLUDED.state,
				vcpu         = EXCLUDED.vcpu,
				mem_bytes    = EXCLUDED.mem_bytes,
				autostart    = EXCLUDED.autostart,
				last_seen_at = EXCLUDED.last_seen_at`,
			hostID, v.Kind, v.ExternalID, v.Name, v.State, v.VCPU, v.MemBytes, v.Autostart, seenAt)
		if err != nil {
			return fmt.Errorf("vms upsert: %w", err)
		}
	}
	// Drop VMs we no longer see for the same kinds.
	if len(kinds) > 0 {
		ks := make([]string, 0, len(kinds))
		for k := range kinds {
			ks = append(ks, k)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM vms WHERE host_id = $1 AND kind = ANY($2) AND last_seen_at < $3`,
			hostID, ks, seenAt); err != nil {
			return fmt.Errorf("vms prune: %w", err)
		}
	}
	return nil
}

func saveUsersTx(ctx context.Context, tx pgx.Tx, hostID uuid.UUID, users []apitypes.UserInfo) error {
	if len(users) == 0 {
		return nil
	}
	seenAt := time.Now().UTC()
	for _, u := range users {
		if u.Username == "" {
			continue
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO observed_users (
				host_id, username, uid, gid, shell, home,
				is_sudoer, is_system, last_login_at, last_seen_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (host_id, username) DO UPDATE SET
				uid           = EXCLUDED.uid,
				gid           = EXCLUDED.gid,
				shell         = EXCLUDED.shell,
				home          = EXCLUDED.home,
				is_sudoer     = EXCLUDED.is_sudoer,
				is_system     = EXCLUDED.is_system,
				last_login_at = COALESCE(EXCLUDED.last_login_at, observed_users.last_login_at),
				last_seen_at  = EXCLUDED.last_seen_at`,
			hostID, u.Username, u.UID, u.GID, nullableString(u.Shell), nullableString(u.Home),
			u.IsSudoer, u.IsSystem, u.LastLoginAt, seenAt)
		if err != nil {
			return fmt.Errorf("observed_users upsert: %w", err)
		}
	}
	// Prune accounts that disappeared since the previous snapshot.
	if _, err := tx.Exec(ctx,
		`DELETE FROM observed_users WHERE host_id = $1 AND last_seen_at < $2`,
		hostID, seenAt); err != nil {
		return fmt.Errorf("observed_users prune: %w", err)
	}
	return nil
}

func saveLoginEventsTx(ctx context.Context, tx pgx.Tx, hostID uuid.UUID, events []apitypes.LoginEvent) error {
	for _, e := range events {
		_, err := tx.Exec(ctx, `
			INSERT INTO login_events (time, host_id, username, source_ip, method, success, detail)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			e.Time, hostID, nullableString(e.Username), nullableString(e.SourceIP),
			e.Method, e.Success, nullableString(e.Detail))
		if err != nil {
			return fmt.Errorf("login_events insert: %w", err)
		}
	}
	return nil
}

func saveSecurityTx(ctx context.Context, tx pgx.Tx, hostID uuid.UUID, sec apitypes.SecurityReport) error {
	for _, fw := range sec.Firewalls {
		_, err := tx.Exec(ctx, `
			INSERT INTO firewall_status (
				host_id, engine, active, default_input, default_output, default_forward,
				rule_count, snapshot_excerpt, snapshot_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())
			ON CONFLICT (host_id, engine) DO UPDATE SET
				active           = EXCLUDED.active,
				default_input    = EXCLUDED.default_input,
				default_output   = EXCLUDED.default_output,
				default_forward  = EXCLUDED.default_forward,
				rule_count       = EXCLUDED.rule_count,
				snapshot_excerpt = EXCLUDED.snapshot_excerpt,
				snapshot_at      = now()`,
			hostID, fw.Engine, fw.Active,
			nullableString(fw.DefaultInput), nullableString(fw.DefaultOutput), nullableString(fw.DefaultForward),
			fw.RuleCount, nullableString(fw.SnapshotExcerpt))
		if err != nil {
			return fmt.Errorf("firewall_status upsert: %w", err)
		}
	}

	if len(sec.Fail2ban) > 0 {
		seenAt := time.Now().UTC()
		for _, j := range sec.Fail2ban {
			_, err := tx.Exec(ctx, `
				INSERT INTO fail2ban_jails (
					host_id, jail, currently_failed, total_failed,
					currently_banned, total_banned, banned_ips, last_seen_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
				ON CONFLICT (host_id, jail) DO UPDATE SET
					currently_failed = EXCLUDED.currently_failed,
					total_failed     = EXCLUDED.total_failed,
					currently_banned = EXCLUDED.currently_banned,
					total_banned     = EXCLUDED.total_banned,
					banned_ips       = EXCLUDED.banned_ips,
					last_seen_at     = EXCLUDED.last_seen_at`,
				hostID, j.Jail, j.CurrentlyFailed, j.TotalFailed,
				j.CurrentlyBanned, j.TotalBanned, j.BannedIPs, seenAt)
			if err != nil {
				return fmt.Errorf("fail2ban_jails upsert: %w", err)
			}
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM fail2ban_jails WHERE host_id = $1 AND last_seen_at < $2`,
			hostID, seenAt); err != nil {
			return fmt.Errorf("fail2ban_jails prune: %w", err)
		}
	}

	if len(sec.CrowdSec) > 0 {
		seenAt := time.Now().UTC()
		for _, d := range sec.CrowdSec {
			_, err := tx.Exec(ctx, `
				INSERT INTO crowdsec_decisions (
					host_id, decision_id, origin, scope, target,
					decision_type, reason, until, last_seen_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
				ON CONFLICT (host_id, decision_id) DO UPDATE SET
					origin        = EXCLUDED.origin,
					scope         = EXCLUDED.scope,
					target        = EXCLUDED.target,
					decision_type = EXCLUDED.decision_type,
					reason        = EXCLUDED.reason,
					until         = EXCLUDED.until,
					last_seen_at  = EXCLUDED.last_seen_at`,
				hostID, d.DecisionID, nullableString(d.Origin), nullableString(d.Scope),
				nullableString(d.Target), nullableString(d.Type), nullableString(d.Reason),
				nilIfZero(d.Until), seenAt)
			if err != nil {
				return fmt.Errorf("crowdsec_decisions upsert: %w", err)
			}
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM crowdsec_decisions WHERE host_id = $1 AND last_seen_at < $2`,
			hostID, seenAt); err != nil {
			return fmt.Errorf("crowdsec_decisions prune: %w", err)
		}
	}
	return nil
}

func nilIfZero(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func uniqueManagers(pkgs []apitypes.InstalledPackage) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, p := range pkgs {
		if _, ok := seen[p.Manager]; ok {
			continue
		}
		seen[p.Manager] = struct{}{}
		out = append(out, p.Manager)
	}
	return out
}

func uniqueUpdateManagers(ups []apitypes.PendingUpdate) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, u := range ups {
		if _, ok := seen[u.Manager]; ok {
			continue
		}
		seen[u.Manager] = struct{}{}
		out = append(out, u.Manager)
	}
	return out
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
