package store

import (
	"strings"
	"testing"
)

func TestEncryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	const plain = "JBSWY3DPEHPK3PXP" // sample base32 TOTP secret

	ct, err := encryptWithKey(key, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(ct, encPrefix) {
		t.Fatalf("ciphertext missing %q prefix: %q", encPrefix, ct)
	}
	if strings.Contains(ct, plain) {
		t.Fatal("plaintext leaked into ciphertext")
	}

	got, err := decryptWithKey(key, ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plain {
		t.Fatalf("round trip mismatch: got %q want %q", got, plain)
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	k1, k2 := make([]byte, 32), make([]byte, 32)
	k2[0] = 1
	ct, err := encryptWithKey(k1, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decryptWithKey(k2, ct); err == nil {
		t.Fatal("expected decrypt with wrong key to fail")
	}
}

func TestDecryptAtRestLegacyPassthrough(t *testing.T) {
	// A value with no versioned prefix is legacy plaintext and must pass
	// through unchanged regardless of key configuration.
	const legacy = "OLDPLAINTEXTSECRET"
	got, err := DecryptAtRest(legacy)
	if err != nil {
		t.Fatalf("passthrough: %v", err)
	}
	if got != legacy {
		t.Fatalf("legacy passthrough mismatch: got %q want %q", got, legacy)
	}
}
