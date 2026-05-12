package store

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

// FuzzHashSecret asserts that hashSecret never panics on arbitrary-length
// input and always returns a 32-byte SHA-256 digest matching the stdlib
// reference. SHA-256 is total over byte sequences, so we lock in equivalence
// rather than just non-panic.
func FuzzHashSecret(f *testing.F) {
	f.Add("")
	f.Add("mon_bs_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	f.Add("mon_ag_xxxx")
	f.Add("\x00\x01\x02\xff")
	// A multi-KB payload makes sure we don't have a hidden length cap.
	f.Add(string(bytes.Repeat([]byte{'a'}, 4096)))

	f.Fuzz(func(t *testing.T, s string) {
		got := hashSecret(s)
		if len(got) != sha256.Size {
			t.Fatalf("hashSecret(%q) returned %d bytes, want %d", s, len(got), sha256.Size)
		}
		want := sha256.Sum256([]byte(s))
		if !bytes.Equal(got, want[:]) {
			t.Fatalf("hashSecret(%q) mismatch with stdlib", s)
		}
	})
}
