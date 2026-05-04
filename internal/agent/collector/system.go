package collector

import (
	"context"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

type System struct {
	// last is used to compute usage between consecutive Collects.
	lastCPU []cpu.TimesStat
}

func NewSystem() *System { return &System{} }

func (System) Name() string { return "system" }

func (s *System) Inventory(ctx context.Context, snap *apitypes.InventorySnap) error {
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return err
	}
	snap.Hostname = info.Hostname
	snap.Kernel = info.KernelVersion
	if info.Platform != "" {
		snap.Distro = info.Platform + " " + info.PlatformVersion
	}

	cpus, err := cpu.InfoWithContext(ctx)
	if err == nil && len(cpus) > 0 {
		snap.CPUModel = cpus[0].ModelName
	}
	snap.CPUCores = runtime.NumCPU()

	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		snap.RAMTotalBytes = int64(vm.Total) //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
	}
	return nil
}

func (s *System) Collect(ctx context.Context, batch *apitypes.IngestRequest) error {
	now := time.Now().UTC()

	pct, _ := cpu.PercentWithContext(ctx, 0, false)
	perCore, _ := cpu.PercentWithContext(ctx, 0, true)
	la, _ := load.AvgWithContext(ctx)
	vm, _ := mem.VirtualMemoryWithContext(ctx)
	sm, _ := mem.SwapMemoryWithContext(ctx)
	uptime, _ := host.UptimeWithContext(ctx)

	sample := apitypes.SystemSample{
		Time:       now,
		CPUPerCore: perCore,
		UptimeSec:  int64(uptime), //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
	}
	if len(pct) > 0 {
		sample.CPUUsagePct = pct[0]
	}
	if la != nil {
		sample.Load1 = la.Load1
		sample.Load5 = la.Load5
		sample.Load15 = la.Load15
	}
	if vm != nil {
		sample.RAMUsedBytes = int64(vm.Used)       //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
		sample.RAMAvailBytes = int64(vm.Available) //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
	}
	if sm != nil {
		sample.SwapUsedBytes = int64(sm.Used) //nolint:gosec // uint64 from gopsutil/docker; bytes/packets fit in int64
	}

	batch.System = append(batch.System, sample)
	return nil
}
