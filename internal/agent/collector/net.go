package collector

import (
	"context"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/net"

	"github.com/pr0ph37/mon/internal/shared/apitypes"
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
		snap.Nics = append(snap.Nics, apitypes.NicInfo{
			Name: ifc.Name,
			MAC:  ifc.HardwareAddr,
		})
	}
	return nil
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
