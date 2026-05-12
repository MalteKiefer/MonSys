package store

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// makePackages builds n InstalledPackages of plausible shape (3 managers in
// round-robin, monotonic names, fixed versions/arch). Reused by every
// package-shaped benchmark so payload sizes are comparable.
func makePackages(n int) []apitypes.InstalledPackage {
	managers := []string{"dpkg", "rpm", "pacman"}
	out := make([]apitypes.InstalledPackage, n)
	for i := range out {
		out[i] = apitypes.InstalledPackage{
			Manager:    managers[i%len(managers)],
			Name:       fmt.Sprintf("libfoo-%d", i),
			Version:    "1.2.3-4",
			Arch:       "amd64",
			SourceRepo: "main",
		}
	}
	return out
}

// makePendingUpdates is the PendingUpdate analogue of makePackages, with
// a is_security flip every 5th row to keep both branches warm.
func makePendingUpdates(n int) []apitypes.PendingUpdate {
	managers := []string{"dpkg", "rpm", "pacman", "apk"}
	out := make([]apitypes.PendingUpdate, n)
	for i := range out {
		out[i] = apitypes.PendingUpdate{
			Manager:          managers[i%len(managers)],
			Name:             fmt.Sprintf("libfoo-%d", i),
			CurrentVersion:   "1.2.3-4",
			AvailableVersion: "1.2.3-5",
			Arch:             "amd64",
			IsSecurity:       i%5 == 0,
		}
	}
	return out
}

// makeSystemSamples is the SystemSample shape mon-agent batches between
// /v1/ingest calls (typical: 1 per tick for 60s × N hosts).
func makeSystemSamples(n int) []apitypes.SystemSample {
	t0 := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	out := make([]apitypes.SystemSample, n)
	for i := range out {
		out[i] = apitypes.SystemSample{
			Time:          t0.Add(time.Duration(i) * time.Second),
			CPUUsagePct:   42.5,
			CPUPerCore:    []float64{40, 41, 42, 43, 44, 45, 46, 47},
			Load1:         1.5,
			Load5:         1.8,
			Load15:        2.1,
			RAMUsedBytes:  68719476736,
			RAMAvailBytes: 68719476736,
			SwapUsedBytes: 1073741824,
			UptimeSec:     86400 + int64(i),
		}
	}
	return out
}

// BenchmarkIngestPackages measures the per-batch CPU + alloc cost of the
// non-DB prep work inside savePackagesTx for various batch sizes. We
// cannot run the real tx.Exec without a Postgres, but the uniqueManagers
// scan + json/time/string marshalling done before each Exec is what
// dominates CPU when the batch is wide (typical dpkg dump: ~3000 rows).
//
// Sizes mirror the spec: 1 / 10 / 100 / 1000. The 10 000 case is added
// because a Debian/Ubuntu host with many libraries hits that.
func BenchmarkIngestPackages(b *testing.B) {
	sizes := []int{1, 10, 100, 1000, 10000}
	for _, n := range sizes {
		pkgs := makePackages(n)
		ups := makePendingUpdates(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// What savePackagesTx does on every batch before issuing
				// SQL: build the deduped manager lists used in the
				// DELETE WHERE manager = ANY(...) prune.
				_ = uniqueManagers(pkgs)
				_ = uniqueUpdateManagers(ups)
				// Plus the per-row nullableString and nilIfZero
				// adapters (called once per row inside the loop).
				for j := range pkgs {
					_ = nullableString(pkgs[j].SourceRepo)
					_ = nilIfZero(time.Time{})
				}
			}
		})
	}
}

// BenchmarkIngestSystemSample measures the JSON-shape cost of a batch
// of SystemSamples — what we pay before any of them reach pgx. With
// table-driven sizes operators can spot a regression (e.g. someone
// adds a heavy field) at the agent-encode or server-decode boundary.
func BenchmarkIngestSystemSample(b *testing.B) {
	sizes := []int{1, 10, 100, 1000}
	for _, n := range sizes {
		samples := makeSystemSamples(n)
		b.Run(fmt.Sprintf("encode-n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = json.Marshal(samples)
			}
		})
		data, _ := json.Marshal(samples)
		b.Run(fmt.Sprintf("decode-n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for i := 0; i < b.N; i++ {
				var out []apitypes.SystemSample
				if err := json.Unmarshal(data, &out); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkOrEmpty / nilIfZero are tiny helpers but they're called
// once per ingest row, so their per-call cost compounds. Lock in a
// baseline so a regression (e.g. switching to a reflect-based helper)
// is caught.
func BenchmarkOrEmpty(b *testing.B) {
	in := map[string]string{"k1": "v1", "k2": "v2", "k3": "v3"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = orEmpty(in)
	}
}

func BenchmarkNilIfZero(b *testing.B) {
	now := time.Now()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = nilIfZero(now)
		_ = nilIfZero(time.Time{})
	}
}
