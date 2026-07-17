//go:build linux

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMailEnabled_DefaultsToTrue(t *testing.T) {
	// No mail block in YAML → MailEnabled() must return true.
	yaml := `server_url: http://example.com`
	cfg := loadYAML(t, yaml)
	if !cfg.MailEnabled() {
		t.Error("MailEnabled() = false; want true when mail block is absent")
	}
}

func TestRspamdStatURL_DefaultValue(t *testing.T) {
	// No mail block in YAML → RspamdStatURL() must return the default.
	yaml := `server_url: http://example.com`
	cfg := loadYAML(t, yaml)
	const want = "http://127.0.0.1:11334/stat"
	if got := cfg.RspamdStatURL(); got != want {
		t.Errorf("RspamdStatURL() = %q; want %q", got, want)
	}
}

func TestMailEnabled_ExplicitFalse(t *testing.T) {
	// mail.enabled: false → MailEnabled() must return false.
	yaml := "server_url: http://example.com\nmail:\n  enabled: false\n"
	cfg := loadYAML(t, yaml)
	if cfg.MailEnabled() {
		t.Error("MailEnabled() = true; want false when mail.enabled is false")
	}
}

// loadYAML writes yamlContent to a temp file and loads it via Load so the
// same YAML loader the package uses in production is exercised.
func loadYAML(t *testing.T, yamlContent string) Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	return cfg
}
