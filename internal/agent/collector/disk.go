package collector

import (
	"context"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

type Disk struct{}

func NewDisk() *Disk { return &Disk{} }

func (Disk) Name() string { return "disk" }

// excludedFS lists pseudo / virtual filesystems that we never report on.
var excludedFS = map[string]struct{}{
	"tmpfs": {}, "devtmpfs": {}, "proc": {}, "sysfs": {}, "cgroup": {},
	"cgroup2": {}, "overlay": {}, "squashfs": {}, "autofs": {}, "binfmt_misc": {},
	"devpts": {}, "mqueue": {}, "hugetlbfs": {}, "pstore": {}, "tracefs": {},
	"debugfs": {}, "fusectl": {}, "configfs": {}, "rpc_pipefs": {}, "nsfs": {},
	"securityfs": {}, "fuse.gvfsd-fuse": {}, "ramfs": {},
}

func interesting(p disk.PartitionStat) bool {
	if _, skip := excludedFS[p.Fstype]; skip {
		return false
	}
	if strings.HasPrefix(p.Mountpoint, "/snap/") || strings.HasPrefix(p.Mountpoint, "/var/lib/docker/") {
		return false
	}
	return true
}

func (Disk) Inventory(ctx context.Context, snap *apitypes.InventorySnap) error {
	parts, err := disk.PartitionsWithContext(ctx, true)
	if err != nil {
		return err
	}
	for _, p := range parts {
		if !interesting(p) {
			continue
		}
		usage, _ := disk.UsageWithContext(ctx, p.Mountpoint)
		var size int64
		if usage != nil {
			size = int64(usage.Total)
		}
		snap.Disks = append(snap.Disks, apitypes.DiskInfo{
			Device:     p.Device,
			Mountpoint: p.Mountpoint,
			FSType:     p.Fstype,
			SizeBytes:  size,
		})
	}
	return nil
}

func (Disk) Collect(ctx context.Context, batch *apitypes.IngestRequest) error {
	now := time.Now().UTC()

	parts, err := disk.PartitionsWithContext(ctx, true)
	if err != nil {
		return err
	}

	io, _ := disk.IOCountersWithContext(ctx)

	for _, p := range parts {
		if !interesting(p) {
			continue
		}
		usage, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil {
			continue
		}

		s := apitypes.DiskSample{
			Time:       now,
			Device:     p.Device,
			Mountpoint: p.Mountpoint,
			UsedBytes:  int64(usage.Used),
			FreeBytes:  int64(usage.Free),
			InodesUsed: int64(usage.InodesUsed),
			InodesFree: int64(usage.InodesFree),
		}

		// IO counters are keyed by the bare device name (e.g. "sda" or "nvme0n1").
		// gopsutil already strips the /dev/ prefix in IOCounters but not partition suffix;
		// look up by basename of p.Device.
		if dev, ok := io[basename(p.Device)]; ok {
			s.ReadBytes = int64(dev.ReadBytes)
			s.WriteBytes = int64(dev.WriteBytes)
			s.ReadOps = int64(dev.ReadCount)
			s.WriteOps = int64(dev.WriteCount)
			s.IOTimeMS = int64(dev.IoTime)
		}
		batch.Disks = append(batch.Disks, s)
	}
	return nil
}

func basename(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
