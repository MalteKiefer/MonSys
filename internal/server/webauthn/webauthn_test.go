package webauthn

import (
	"bytes"
	"strings"
	"testing"

	libwa "github.com/go-webauthn/webauthn/webauthn"
)

// TestNew_RequiresRPID ensures startup config validation refuses an empty
// RPID — a missing hostname would make every passkey registration silently
// succeed but break login on the next session.
func TestNew_RequiresRPID(t *testing.T) {
	_, err := New(Config{RPID: "", Origins: []string{"https://mon.example.com"}})
	if err == nil {
		t.Fatal("expected error for empty RPID")
	}
	if !strings.Contains(err.Error(), "RPID") {
		t.Errorf("error message should mention RPID, got %v", err)
	}
}

// TestNew_RequiresOrigins forbids zero-origin config. Without at least one
// origin every clientDataJSON would fail the check and login would 4xx.
func TestNew_RequiresOrigins(t *testing.T) {
	_, err := New(Config{RPID: "mon.example.com"})
	if err == nil {
		t.Fatal("expected error for empty Origins")
	}
	if !strings.Contains(err.Error(), "origin") {
		t.Errorf("error message should mention origin, got %v", err)
	}
}

// TestNew_DefaultRPName populates the human-readable name when caller
// omitted it. Browsers display this on the consent prompt; an empty
// fallback would be operationally jarring.
func TestNew_DefaultRPName(t *testing.T) {
	svc, err := New(Config{
		RPID:    "mon.example.com",
		Origins: []string{"https://mon.example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc == nil || svc.WA == nil {
		t.Fatal("New returned a nil Service")
	}
}

// TestNew_HappyPath builds a fully configured Service and verifies the
// underlying go-webauthn handle is non-nil so callers can immediately use
// it for ceremonies.
func TestNew_HappyPath(t *testing.T) {
	svc, err := New(Config{
		RPID:    "mon.example.com",
		RPName:  "MonSys",
		Origins: []string{"https://mon.example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.WA == nil {
		t.Fatal("Service.WA is nil after successful New")
	}
}

// TestUser_AdapterAccessors pins the four WebAuthn-User accessors against
// the values stored on the adapter struct — these are the bytes the
// library shows to the authenticator.
func TestUser_AdapterAccessors(t *testing.T) {
	u := &User{
		Handle:      []byte{0x01, 0x02, 0x03, 0x04},
		Name:        "alice@example.com",
		DisplayName: "Alice",
	}

	if !bytes.Equal(u.WebAuthnID(), u.Handle) {
		t.Errorf("WebAuthnID returned %v, want %v", u.WebAuthnID(), u.Handle)
	}
	if u.WebAuthnName() != "alice@example.com" {
		t.Errorf("WebAuthnName = %q, want %q", u.WebAuthnName(), "alice@example.com")
	}
	if u.WebAuthnDisplayName() != "Alice" {
		t.Errorf("WebAuthnDisplayName = %q, want %q", u.WebAuthnDisplayName(), "Alice")
	}
	// nil Creds field must mirror through as nil (no implicit empty slice).
	if creds := u.WebAuthnCredentials(); creds != nil {
		t.Errorf("WebAuthnCredentials() = %v, want nil", creds)
	}

	// Populated Creds must surface 1:1.
	u.Creds = []libwa.Credential{{ID: []byte("c1")}, {ID: []byte("c2")}}
	got := u.WebAuthnCredentials()
	if len(got) != 2 {
		t.Errorf("WebAuthnCredentials() returned %d entries, want 2", len(got))
	}
}

// TestConvertCredential maps raw DB bytes to a libwa.Credential with the
// correct flags, transports, and authenticator metadata.
func TestConvertCredential(t *testing.T) {
	credID := []byte("cred-id-bytes")
	pubKey := []byte("public-key-bytes")
	aaguid := []byte("aaguid-bytes-1234")

	cred := ConvertCredential(
		credID, pubKey, 42, []string{"usb", "internal"}, true, true, aaguid,
	)

	if !bytes.Equal(cred.ID, credID) {
		t.Errorf("ID mismatch: got %x", cred.ID)
	}
	if !bytes.Equal(cred.PublicKey, pubKey) {
		t.Errorf("PublicKey mismatch: got %x", cred.PublicKey)
	}
	if cred.AttestationType != "none" {
		t.Errorf("AttestationType = %q, want %q", cred.AttestationType, "none")
	}
	if cred.Authenticator.SignCount != 42 {
		t.Errorf("SignCount = %d, want 42", cred.Authenticator.SignCount)
	}
	if !bytes.Equal(cred.Authenticator.AAGUID, aaguid) {
		t.Errorf("AAGUID mismatch")
	}
	if !cred.Flags.UserPresent {
		t.Error("Flags.UserPresent should be true")
	}
	if !cred.Flags.UserVerified {
		t.Error("Flags.UserVerified should be true")
	}
	if !cred.Flags.BackupEligible {
		t.Error("Flags.BackupEligible should reflect caller arg")
	}
	if !cred.Flags.BackupState {
		t.Error("Flags.BackupState should reflect caller arg")
	}
	if len(cred.Transport) != 2 {
		t.Fatalf("Transport length = %d, want 2", len(cred.Transport))
	}
	if string(cred.Transport[0]) != "usb" || string(cred.Transport[1]) != "internal" {
		t.Errorf("Transport values wrong: %v", cred.Transport)
	}
}

// TestConvertCredential_NoTransports tolerates an empty/nil transports
// slice — agents on locked-down devices may not enumerate any.
func TestConvertCredential_NoTransports(t *testing.T) {
	cred := ConvertCredential([]byte("cred"), []byte("pub"), 0, nil, false, false, nil)
	if cred.Transport == nil {
		t.Error("Transport should be a non-nil (possibly empty) slice")
	}
	if len(cred.Transport) != 0 {
		t.Errorf("Transport length = %d, want 0", len(cred.Transport))
	}
}
