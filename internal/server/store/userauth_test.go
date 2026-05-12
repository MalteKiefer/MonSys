package store

import (
	"testing"
	"time"
)

// TestEmailKey verifies the case-fold/trim contract that prevents an attacker
// from sidestepping the lockout by varying capitalisation or whitespace.
func TestEmailKey(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"alice@example.com", "alice@example.com"},
		{"ALICE@EXAMPLE.COM", "alice@example.com"},
		{"  AlIce@Example.Com  ", "alice@example.com"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := emailKey(c.in); got != c.want {
			t.Errorf("emailKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFailedLoginAttempts_LockoutCycle drives the in-memory tracker through
// the exact path the audit doc describes: record up-to-threshold failures →
// the next failure trips the lockout → ClearFailedLogins drops the lock.
func TestFailedLoginAttempts_LockoutCycle(t *testing.T) {
	tr := NewFailedLoginAttempts()

	const email = "user@example.com"

	if locked, _ := tr.IsLockedOut(email); locked {
		t.Fatalf("fresh tracker reported lockout")
	}

	// Record threshold-1 failures: still not locked out.
	for i := range loginMaxFailedAttempts - 1 {
		tr.RecordFailedLogin(email)
		if locked, _ := tr.IsLockedOut(email); locked {
			t.Fatalf("locked after only %d attempts (limit %d)", i+1, loginMaxFailedAttempts)
		}
	}

	// One more — must trip the lockout.
	tr.RecordFailedLogin(email)
	locked, until := tr.IsLockedOut(email)
	if !locked {
		t.Fatalf("expected lockout after %d attempts", loginMaxFailedAttempts)
	}
	if until.Before(time.Now()) {
		t.Fatalf("lockout deadline %v already in the past", until)
	}
	if until.After(time.Now().Add(loginLockoutDuration + time.Minute)) {
		t.Fatalf("lockout deadline %v too far in the future", until)
	}

	// Case-insensitive: lookup with different casing also locks out.
	if locked2, _ := tr.IsLockedOut("USER@example.com"); !locked2 {
		t.Fatalf("case variant should also be reported locked out")
	}

	// Clear: subsequent lookups are clean.
	tr.ClearFailedLogins(email)
	if locked3, _ := tr.IsLockedOut(email); locked3 {
		t.Fatalf("ClearFailedLogins did not drop the lock")
	}
}

// TestFailedLoginAttempts_NilSafe confirms the public API tolerates a nil
// receiver, which is the documented contract.
func TestFailedLoginAttempts_NilSafe(t *testing.T) {
	var tr *FailedLoginAttempts
	tr.RecordFailedLogin("x@y")
	tr.ClearFailedLogins("x@y")
	tr.GC()
	if locked, until := tr.IsLockedOut("x@y"); locked || !until.IsZero() {
		t.Fatalf("nil tracker reported lockout: locked=%v until=%v", locked, until)
	}
}

// TestFailedLoginAttempts_GC_NoExpired exercises both GC passes on a clean
// state and on state below the cleanup window (so the buckets stay).
func TestFailedLoginAttempts_GC_NoExpired(t *testing.T) {
	tr := NewFailedLoginAttempts()
	tr.RecordFailedLogin("a@b.com")
	tr.RecordFailedLogin("a@b.com")
	tr.GC() // nothing should be removed; we just inserted fresh attempts.
	tr.mu.RLock()
	got := len(tr.attempts)
	tr.mu.RUnlock()
	if got != 1 {
		t.Fatalf("GC dropped fresh bucket: have %d buckets", got)
	}
}

// TestFailedLoginAttempts_GC_DropsStaleAttempts forces stale entries by
// reaching directly into the map (white-box) and calling gcStaleAttempts.
func TestFailedLoginAttempts_GC_DropsStaleAttempts(t *testing.T) {
	tr := NewFailedLoginAttempts()
	key := emailKey("stale@example.com")
	old := time.Now().Add(-2 * loginAttemptWindow)
	tr.attempts[key] = []time.Time{old}

	tr.gcStaleAttempts(time.Now().Add(-loginAttemptWindow))

	tr.mu.RLock()
	_, present := tr.attempts[key]
	tr.mu.RUnlock()
	if present {
		t.Fatalf("expected gcStaleAttempts to drop stale bucket")
	}
}

// TestFailedLoginAttempts_GC_DropsExpiredLockouts exercises the lockout-
// cleanup pass directly.
func TestFailedLoginAttempts_GC_DropsExpiredLockouts(t *testing.T) {
	tr := NewFailedLoginAttempts()
	key := emailKey("expired@example.com")
	past := time.Now().Add(-time.Minute)
	tr.lockedAt[key] = past
	tr.attempts[key] = []time.Time{past}

	tr.gcExpiredLockouts(time.Now())

	tr.mu.RLock()
	_, lockPresent := tr.lockedAt[key]
	_, attPresent := tr.attempts[key]
	tr.mu.RUnlock()
	if lockPresent || attPresent {
		t.Fatalf("expected gcExpiredLockouts to remove both lock and attempts")
	}
}

// TestPgIsUniqueViolation pins the unique-violation matcher to known
// Postgres error texts, including the bare SQLSTATE form.
func TestPgIsUniqueViolation(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errStub(""), false},
		{errStub("duplicate key value violates unique constraint \"users_email_key\""), true},
		{errStub("SQLSTATE 23505"), true},
		{errStub("foreign key violation"), false},
	}
	for i, c := range cases {
		got := pgIsUniqueViolation(c.err)
		if got != c.want {
			t.Errorf("[%d] pgIsUniqueViolation(%v) = %v, want %v", i, c.err, got, c.want)
		}
	}
}

// errStub is a trivial error type so the table above can compose error values
// without importing fmt or errors.New everywhere.
type errStub string

func (e errStub) Error() string { return string(e) }
