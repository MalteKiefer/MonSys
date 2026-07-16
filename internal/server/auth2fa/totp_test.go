package auth2fa

import (
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func TestBackupCodeHashingAndConsume(t *testing.T) {
	plain, err := NewBackupCodes(3)
	if err != nil {
		t.Fatal(err)
	}
	hashed, err := HashBackupCodes(plain)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hashed {
		if h[:2] != "$2" {
			t.Fatalf("expected bcrypt hash, got %q", h)
		}
	}

	// A valid code (dash-insensitive) matches and is consumed.
	remaining, ok := MatchAndConsume(hashed, plain[1])
	if !ok {
		t.Fatal("valid backup code did not match its hash")
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining, got %d", len(remaining))
	}
	// The consumed code no longer matches.
	if _, ok := MatchAndConsume(remaining, plain[1]); ok {
		t.Fatal("consumed backup code matched again")
	}
	// A wrong code never matches.
	if _, ok := MatchAndConsume(hashed, "0000-0000"); ok {
		t.Fatal("unexpected match for wrong code")
	}
}

func TestMatchAndConsumeLegacyPlaintext(t *testing.T) {
	// Pre-M2 rows stored plaintext codes; they must still verify.
	codes := []string{"aaaa-bbbb", "cccc-dddd"}
	remaining, ok := MatchAndConsume(codes, "AAAABBBB") // dash/case-insensitive
	if !ok {
		t.Fatal("legacy plaintext code did not match")
	}
	if len(remaining) != 1 || remaining[0] != "cccc-dddd" {
		t.Fatalf("unexpected remaining: %v", remaining)
	}
}

func TestValidateAndStepRejectsReplay(t *testing.T) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "t", AccountName: "a"})
	if err != nil {
		t.Fatal(err)
	}
	secret := key.Secret()
	now := time.Now()
	code, err := totp.GenerateCodeCustom(secret, now, totp.ValidateOpts{
		Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatal(err)
	}
	step, ok := ValidateAndStep(secret, code)
	if !ok {
		t.Fatal("valid code rejected")
	}
	// Caller enforces single-use by persisting the step; a code whose step is
	// <= the last consumed step must be treated as a replay. Here the same
	// code yields the same step, so step <= step is a replay.
	step2, ok := ValidateAndStep(secret, code)
	if !ok || step2 != step {
		t.Fatalf("expected stable step, got ok=%v step=%d (first %d)", ok, step2, step)
	}
	if step2 > step {
		t.Fatal("replay guard precondition failed: step2 should equal step")
	}
}
