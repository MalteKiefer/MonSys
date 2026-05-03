package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pr0ph37/mon/internal/shared/apitypes"
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

type diskKey struct{ Device, Mountpoint string }
type workloadKey struct{ Kind, ExternalID string }

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
		_, err := tx.Exec(ctx, `
			INSERT INTO nics (host_id, name, mac, speed_mbps)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (host_id, name) DO UPDATE SET
				mac          = EXCLUDED.mac,
				speed_mbps   = EXCLUDED.speed_mbps,
				last_seen_at = now()`,
			hostID, n.Name, n.MAC, n.SpeedMbps)
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
		_, err = tx.Exec(ctx, `
			INSERT INTO workloads (host_id, kind, external_id, name, image, state, labels)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (host_id, kind, external_id) DO UPDATE SET
				name         = EXCLUDED.name,
				image        = EXCLUDED.image,
				state        = EXCLUDED.state,
				labels       = EXCLUDED.labels,
				last_seen_at = now()`,
			hostID, w.Kind, w.ExternalID, w.Name, w.Image, w.State, labelsJSON)
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

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
