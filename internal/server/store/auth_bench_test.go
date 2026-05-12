package store

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// BenchmarkBcryptHash measures the wall-clock cost of bcrypt at the
// configured cost (12). Login latency is dominated by this hash plus the
// session round-trip, so a regression here translates 1:1 into worse
// login response time. Healthy range on modern hardware is roughly
// 200–400 ms per hash; orders-of-magnitude faster suggests the cost was
// accidentally lowered.
//
// Note: this is intentionally slow (b.N stays small). Run with
// `-benchtime=10x` if you want a fixed number of iterations.
func BenchmarkBcryptHash(b *testing.B) {
	pw := []byte("a-realistic-passphrase-with-enough-entropy-1234")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := bcrypt.GenerateFromPassword(pw, bcryptCost); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBcryptCompare measures the inverse path: verifying a password
// against a stored hash. AuthenticateUser is on the hot path for every
// login (including the dummy-compare we run during lockout to keep the
// timing channel closed), so the per-call cost should match the hash
// benchmark above.
func BenchmarkBcryptCompare(b *testing.B) {
	pw := []byte("a-realistic-passphrase-with-enough-entropy-1234")
	hash, err := bcrypt.GenerateFromPassword(pw, bcryptCost)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := bcrypt.CompareHashAndPassword(hash, pw); err != nil {
			b.Fatal(err)
		}
	}
}
