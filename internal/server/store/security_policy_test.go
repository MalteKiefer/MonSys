package store

import (
	"testing"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// TestIsValidForceMode pins the three accepted force-mode strings and refuses
// everything else. Anything other than these constants is operator-supplied
// JSON we never want to let into the SecurityPolicy.
func TestIsValidForceMode(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{ForceModeOff, true},
		{ForceMode2FAAny, true},
		{ForceModePasskeyRequired, true},
		{"", false},
		{"OFF", false},
		{"strict", false},
		{"; DROP TABLE users; --", false},
	}
	for _, c := range cases {
		if got := isValidForceMode(c.in); got != c.want {
			t.Errorf("isValidForceMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestValidateSecurityPolicy walks every bound the validator enforces so a
// future schema tweak that loosens or tightens a range trips here.
func TestValidateSecurityPolicy(t *testing.T) {
	base := apitypes.SecurityPolicy{
		ForceMode:          ForceModeOff,
		GraceDays:          7,
		MaxSessionHours:    12,
		IdleTimeoutMinutes: 0,
	}

	cases := []struct {
		name    string
		mutate  func(*apitypes.SecurityPolicy)
		wantErr bool
	}{
		{"baseline ok", func(_ *apitypes.SecurityPolicy) {}, false},
		{"unknown force_mode", func(p *apitypes.SecurityPolicy) { p.ForceMode = "strict" }, true},
		{"grace_days negative", func(p *apitypes.SecurityPolicy) { p.GraceDays = -1 }, true},
		{"grace_days 0 ok", func(p *apitypes.SecurityPolicy) { p.GraceDays = 0 }, false},
		{"grace_days max", func(p *apitypes.SecurityPolicy) { p.GraceDays = 365 }, false},
		{"grace_days too big", func(p *apitypes.SecurityPolicy) { p.GraceDays = 366 }, true},
		{"max_session_hours 0", func(p *apitypes.SecurityPolicy) { p.MaxSessionHours = 0 }, true},
		{"max_session_hours 1", func(p *apitypes.SecurityPolicy) { p.MaxSessionHours = 1 }, false},
		{"max_session_hours max", func(p *apitypes.SecurityPolicy) { p.MaxSessionHours = 8760 }, false},
		{"max_session_hours too big", func(p *apitypes.SecurityPolicy) { p.MaxSessionHours = 8761 }, true},
		{"idle_timeout 0 ok", func(p *apitypes.SecurityPolicy) { p.IdleTimeoutMinutes = 0 }, false},
		{"idle_timeout max", func(p *apitypes.SecurityPolicy) { p.IdleTimeoutMinutes = 10080 }, false},
		{"idle_timeout too big", func(p *apitypes.SecurityPolicy) { p.IdleTimeoutMinutes = 10081 }, true},
		{"idle_timeout negative", func(p *apitypes.SecurityPolicy) { p.IdleTimeoutMinutes = -1 }, true},
	}

	for _, tc := range cases {
		p := base
		tc.mutate(&p)
		err := validateSecurityPolicy(p)
		if tc.wantErr && err == nil {
			t.Errorf("[%s] expected error, got nil", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("[%s] unexpected error: %v", tc.name, err)
		}
	}
}

// TestDefaultSecurityPolicy locks in the spec-mandated defaults. A change here
// is intentional and must be paired with a docs/changelog update.
func TestDefaultSecurityPolicy(t *testing.T) {
	d := defaultSecurityPolicy
	if d.ForceMode != ForceModeOff {
		t.Errorf("default ForceMode = %q, want %q", d.ForceMode, ForceModeOff)
	}
	if d.GraceDays != 7 {
		t.Errorf("default GraceDays = %d, want 7", d.GraceDays)
	}
	if d.MaxSessionHours != 12 {
		t.Errorf("default MaxSessionHours = %d, want 12", d.MaxSessionHours)
	}
	if d.IdleTimeoutMinutes != 0 {
		t.Errorf("default IdleTimeoutMinutes = %d, want 0", d.IdleTimeoutMinutes)
	}
	// And the defaults themselves must satisfy the validator.
	if err := validateSecurityPolicy(d); err != nil {
		t.Errorf("default policy fails own validator: %v", err)
	}
}

// TestForceModeConstants ensures the ForceMode* string constants do not drift
// from the values stored on disk. Changing any of these silently would
// break the deployed JSON blob.
func TestForceModeConstants(t *testing.T) {
	if ForceModeOff != "off" {
		t.Errorf("ForceModeOff = %q, want \"off\"", ForceModeOff)
	}
	if ForceMode2FAAny != "2fa_any" {
		t.Errorf("ForceMode2FAAny = %q, want \"2fa_any\"", ForceMode2FAAny)
	}
	if ForceModePasskeyRequired != "passkey_required" {
		t.Errorf("ForceModePasskeyRequired = %q, want \"passkey_required\"", ForceModePasskeyRequired)
	}
}
