package apitypes

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// buildIngestRequest constructs a realistic-shape IngestRequest with the
// supplied numbers of system samples and packages. We use this for both
// marshal and unmarshal benchmarks so the payload size is comparable.
// The shape mirrors what mon-agent sends every collector tick (system,
// disk, nic, workload samples + a package report). One-call helper because
// the same shape is reused across benchmarks.
func buildIngestRequest(systemSamples, packages int) IngestRequest {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	req := IngestRequest{
		SnapshotAt: now,
		Inventory: &InventorySnap{
			Hostname:      "bench-host.example.com",
			Kernel:        "6.6.0-12-generic",
			Distro:        "Ubuntu 24.04 LTS",
			AgentVersion:  "1.2.3",
			CPUModel:      "Intel(R) Xeon(R) Gold 6248R CPU @ 3.00GHz",
			CPUCores:      48,
			RAMTotalBytes: 137438953472,
			Disks: []DiskInfo{
				{Device: "/dev/nvme0n1p1", Mountpoint: "/", FSType: "ext4", SizeBytes: 1099511627776},
				{Device: "/dev/nvme0n1p2", Mountpoint: "/home", FSType: "ext4", SizeBytes: 549755813888},
			},
			Nics: []NicInfo{
				{Name: "eno1", MAC: "aa:bb:cc:dd:ee:ff", SpeedMbps: 10000, Addrs: []string{"10.0.0.5/24", "fe80::1/64"}},
			},
		},
	}
	req.System = make([]SystemSample, systemSamples)
	for i := range req.System {
		req.System[i] = SystemSample{
			Time:          now.Add(time.Duration(i) * time.Second),
			CPUUsagePct:   12.5 + float64(i%50),
			CPUPerCore:    []float64{12.1, 13.2, 14.3, 15.4, 16.5, 17.6, 18.7, 19.8},
			Load1:         1.5,
			Load5:         1.8,
			Load15:        2.1,
			RAMUsedBytes:  68719476736,
			RAMAvailBytes: 68719476736,
			SwapUsedBytes: 1073741824,
			UptimeSec:     86400 + int64(i),
		}
	}
	req.Disks = make([]DiskSample, systemSamples)
	for i := range req.Disks {
		req.Disks[i] = DiskSample{
			Time:       now.Add(time.Duration(i) * time.Second),
			Device:     "/dev/nvme0n1p1",
			Mountpoint: "/",
			UsedBytes:  549755813888,
			FreeBytes:  549755813888,
			ReadBytes:  int64(i) * 4096,
			WriteBytes: int64(i) * 8192,
			ReadOps:    int64(i),
			WriteOps:   int64(i),
		}
	}
	req.Nics = make([]NetSample, systemSamples)
	for i := range req.Nics {
		req.Nics[i] = NetSample{
			Time:    now.Add(time.Duration(i) * time.Second),
			NicName: "eno1",
			RxBytes: int64(i) * 1500,
			TxBytes: int64(i) * 1500,
			RxPkts:  int64(i),
			TxPkts:  int64(i),
		}
	}
	if packages > 0 {
		pkgs := make([]InstalledPackage, packages)
		for i := range pkgs {
			pkgs[i] = InstalledPackage{
				Manager: "dpkg",
				Name:    fmt.Sprintf("libfoo-%d", i),
				Version: "1.0.0-1ubuntu1",
				Arch:    "amd64",
			}
		}
		req.Packages = &PackageReport{
			Time:      now,
			StateHash: "abc123",
			Installed: pkgs,
			Summary: PackageSummary{
				InstalledCount:  packages,
				UpdatesCount:    5,
				SecurityUpdates: 1,
			},
		}
	}
	return req
}

// BenchmarkIngestRequestMarshal measures the JSON-encode cost on the agent
// side of the wire (mon-agent encodes once per ingest tick). The
// table-driven sizes cover the typical small batch (10 samples, 0
// packages) and a "full inventory + 1000 packages + 100 samples" worst
// case where a freshly-rebooted host pushes everything at once.
func BenchmarkIngestRequestMarshal(b *testing.B) {
	cases := []struct {
		name      string
		samples   int
		packages  int
	}{
		{"small-10s-0p", 10, 0},
		{"medium-100s-100p", 100, 100},
		{"large-1000s-1000p", 1000, 1000},
		{"realistic-100h-20s", 20, 0},
	}
	for _, c := range cases {
		req := buildIngestRequest(c.samples, c.packages)
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = json.Marshal(req)
			}
		})
	}
}

// BenchmarkIngestRequestUnmarshal measures the JSON-decode cost on the
// server side of the wire (mon-server decodes once per /v1/ingest call).
// Pre-encode once outside the loop so the benchmark measures only decode
// allocations + parse time.
func BenchmarkIngestRequestUnmarshal(b *testing.B) {
	cases := []struct {
		name      string
		samples   int
		packages  int
	}{
		{"small-10s-0p", 10, 0},
		{"medium-100s-100p", 100, 100},
		{"large-1000s-1000p", 1000, 1000},
		{"realistic-100h-20s", 20, 0},
	}
	for _, c := range cases {
		data, err := json.Marshal(buildIngestRequest(c.samples, c.packages))
		if err != nil {
			b.Fatal(err)
		}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for i := 0; i < b.N; i++ {
				var out IngestRequest
				if err := json.Unmarshal(data, &out); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
