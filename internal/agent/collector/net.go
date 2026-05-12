package collector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/net"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

type Net struct{}

func NewNet() *Net { return &Net{} }

func (Net) Name() string { return "net" }

func skipNic(name string) bool {
	switch {
	case name == "lo":
		return true
	case strings.HasPrefix(name, "veth"),
		strings.HasPrefix(name, "docker"),
		strings.HasPrefix(name, "br-"),
		strings.HasPrefix(name, "cni"),
		strings.HasPrefix(name, "flannel"),
		strings.HasPrefix(name, "kube-"):
		return true
	}
	return false
}

func (Net) Inventory(ctx context.Context, snap *apitypes.InventorySnap) error {
	ifaces, err := net.InterfacesWithContext(ctx)
	if err != nil {
		return err
	}
	for _, ifc := range ifaces {
		if skipNic(ifc.Name) {
			continue
		}
		addrs := make([]string, 0, len(ifc.Addrs))
		for _, a := range ifc.Addrs {
			if a.Addr == "" {
				continue
			}
			addrs = append(addrs, a.Addr)
		}
		members, master := bridgeBondTopology(ifc.Name)
		snap.Nics = append(snap.Nics, apitypes.NicInfo{
			Name:         ifc.Name,
			MAC:          ifc.HardwareAddr,
			Addrs:        addrs,
			Members:      members,
			BridgeMaster: master,
		})
	}
	return nil
}

// bridgeBondTopology consults /sys/class/net/<name> to discover whether the
// interface is a bridge or bond master (Members) and/or enslaved to one
// (BridgeMaster). Errors are swallowed: missing entries are normal for plain
// NICs and on non-Linux platforms.
func bridgeBondTopology(name string) (members []string, master string) {
	base := "/sys/class/net/" + name

	// Bridge: members live as symlinks under brif/.
	if entries, err := os.ReadDir(filepath.Join(base, "brif")); err == nil {
		members = make([]string, 0, len(entries))
		for _, e := range entries {
			if n := e.Name(); n != "" {
				members = append(members, n)
			}
		}
	}

	// Bond: slaves are space-separated in bonding/slaves.
	if len(members) == 0 {
		if data, err := os.ReadFile(filepath.Join(base, "bonding", "slaves")); err == nil {
			members = append(members, strings.Fields(string(data))...)
		}
	}

	// master is a symlink to the enslaving iface (bridge or bond).
	if target, err := os.Readlink(filepath.Join(base, "master")); err == nil {
		if b := filepath.Base(target); b != "" && b != "." && b != "/" {
			master = b
		}
	}
	return members, master
}

func (Net) Collect(ctx context.Context, batch *apitypes.IngestRequest) error {
	now := time.Now().UTC()
	stats, err := net.IOCountersWithContext(ctx, true)
	if err != nil {
		return err
	}
	for _, s := range stats {
		if skipNic(s.Name) {
			continue
		}
		batch.Nics = append(batch.Nics, apitypes.NetSample{
			Time:    now,
			NicName: s.Name,
			RxBytes: int64(s.BytesRecv),   //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
			TxBytes: int64(s.BytesSent),   //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
			RxPkts:  int64(s.PacketsRecv), //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
			TxPkts:  int64(s.PacketsSent), //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
			RxErrs:  int64(s.Errin),       //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
			TxErrs:  int64(s.Errout),      //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
			RxDrops: int64(s.Dropin),      //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
			TxDrops: int64(s.Dropout),     //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
		})
	}
	return nil
}
