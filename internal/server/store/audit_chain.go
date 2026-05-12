package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"
)

// VerifyAuditChain walks audit_log rows in (at ASC, id ASC) order and confirms
// the SHA-256 hash chain installed by migration 0026 is intact. The chain rule:
//
//	hash_N = sha256( prev_hash_{N-1} || actor || action || target ||
//	                 detail::text || at::text )
//
// where prev_hash for the first row is 32 zero bytes, and NULL string columns
// are coalesced to ”. The actual hash bytes were produced by Postgres'
// pgcrypto digest() in the BEFORE INSERT trigger, so to avoid timestamp-format
// drift between Go and Postgres we ask Postgres to recompute the canonical
// byte string; we then sha256 it in Go and compare to the stored hash. That
// way the chain integrity check uses Go's crypto, not the database's.
//
// Returns the number of rows scanned. On the first mismatch (or row with a
// NULL/short hash, or where prev_hash doesn't match the previous row's hash),
// brokenAt is set to that row's `at` and the function returns a nil error
// with the count of rows examined up to and including the broken row. On
// success, brokenAt is the zero time.Time.
func (s *Store) VerifyAuditChain(ctx context.Context) (rows int, brokenAt time.Time, err error) {
	const q = `
        SELECT id,
               at,
               hash,
               prev_hash,
               COALESCE(actor,  '')           ||
               COALESCE(action, '')           ||
               COALESCE(target, '')           ||
               COALESCE(detail::text, '')     ||
               at::text                       AS payload_tail
          FROM audit_log
         ORDER BY at ASC, id ASC
    `

	r, err := s.Pool.Query(ctx, q)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("audit chain query: %w", err)
	}
	defer r.Close()

	zeroSeed := make([]byte, 32)
	prevExpected := zeroSeed

	for r.Next() {
		var (
			id          int64
			at          time.Time
			storedHash  []byte
			storedPrev  []byte
			payloadTail string
		)
		if err := r.Scan(&id, &at, &storedHash, &storedPrev, &payloadTail); err != nil {
			return rows, time.Time{}, fmt.Errorf("audit chain scan: %w", err)
		}
		rows++

		// prev_hash on disk must match the running expected previous hash.
		if !equalBytes(storedPrev, prevExpected) {
			return rows, at, nil
		}
		// Recompute hash with Go's sha256 over the canonical payload.
		h := sha256.New()
		h.Write(storedPrev)
		h.Write([]byte(payloadTail))
		want := h.Sum(nil)

		if !equalBytes(storedHash, want) {
			return rows, at, nil
		}
		prevExpected = storedHash
	}
	if err := r.Err(); err != nil {
		return rows, time.Time{}, fmt.Errorf("audit chain rows: %w", err)
	}
	return rows, time.Time{}, nil
}

// equalBytes is a constant-time-free byte comparison; we don't need timing
// safety here because the hashes are non-secret integrity checksums.
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
