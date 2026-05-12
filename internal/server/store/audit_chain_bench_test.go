package store

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// BenchmarkHashChain measures the per-row CPU cost of VerifyAuditChain
// minus the DB roundtrip: sha256(prev_hash || payload_tail) and the
// constant-row equalBytes compare. We can't easily benchmark the full
// function without a live Postgres, but the hash is the dominant
// in-process cost — at N=10k rows on a busy audit_log this is what we'd
// notice if it ever regressed (e.g. someone swapped sha256 for a slower
// hash).
//
// Three sizes: a tiny row (rare — log lines without a detail blob), a
// typical row (~256 B), and a fat one (4 KiB detail blob).
func BenchmarkHashChain(b *testing.B) {
	cases := []struct {
		name        string
		payloadSize int
	}{
		{"tiny-32B", 32},
		{"typical-256B", 256},
		{"fat-4KiB", 4096},
	}
	for _, c := range cases {
		payload := make([]byte, c.payloadSize)
		for i := range payload {
			payload[i] = byte('a' + (i % 26))
		}
		prev := make([]byte, 32)
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(c.payloadSize + 32))
			for i := 0; i < b.N; i++ {
				h := sha256.New()
				h.Write(prev)
				h.Write(payload)
				want := h.Sum(nil)
				if !equalBytes(want, want) {
					b.Fatal("equalBytes self-mismatch")
				}
				prev = want
			}
		})
	}
}

// BenchmarkEqualBytes locks in the small-allocation cost of the byte
// comparison helper. equalBytes runs twice per audit row (prev_hash
// match + computed-hash match), so a regression here multiplies by 2×
// rows scanned during a chain verify.
func BenchmarkEqualBytes(b *testing.B) {
	a := make([]byte, 32)
	c := make([]byte, 32)
	for i := range a {
		a[i] = byte(i)
		c[i] = byte(i)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !equalBytes(a, c) {
			b.Fatal("expected equal")
		}
	}
	_ = fmt.Sprintf // avoid unused import warnings if we later add diagnostic prints
}
