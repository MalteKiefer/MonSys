//go:build linux

// Package config defines the on-disk YAML schema and runtime defaults for the
// mon-agent process. The defaults assume a Linux filesystem layout
// (/etc/mon-agent, /var/lib/mon-agent); a future config_windows.go can supply
// Windows-appropriate paths via the same Config type.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// defaultRspamdStatURL is the Rspamd statistics endpoint assumed when the YAML
// omits mail.rspamd_stat_url. Rspamd listens on 11334 by default.
const defaultRspamdStatURL = "http://127.0.0.1:11334/stat"

// Default knobs applied when the YAML omits the corresponding field. These are
// intentionally conservative — small enough that an idle host stays cheap, but
// large enough that a busy host can buffer through a multi-hour outage.
const (
	// defaultIntervalSeconds is the metric collection cadence when the YAML
	// omits interval_seconds. 15s matches Prometheus' default scrape.
	defaultIntervalSeconds = 15
	// defaultBufferMaxMB caps on-disk spool growth during prolonged server
	// outages. ~256 MiB ≈ several hours of metrics for a typical host.
	defaultBufferMaxMB = 256
	// defaultBufferDir holds the on-disk spool. /var/lib is the standard
	// Linux location for variable state owned by a daemon.
	defaultBufferDir = "/var/lib/mon-agent/spool"
	// defaultKeyFile holds the agent's persisted credential. /etc is used
	// (rather than /var/lib) because the secret lives with the agent's
	// configuration, not its runtime state.
	defaultKeyFile = "/etc/mon-agent/agent.key"
	// defaultPackageUpdateCheckInterval bounds how often the agent runs
	// `apt list --upgradable` / `dnf check-update` / etc. Package indexes
	// don't churn often, and these calls touch the network.
	defaultPackageUpdateCheckInterval = 30 * time.Minute
	// defaultPackageFullSnapshotMaxInterval bounds how stale the full
	// installed-package list may get. A daily resync catches drift even when
	// no upgrades happened.
	defaultPackageFullSnapshotMaxInterval = 24 * time.Hour
	// defaultFallbackInterval is the floor returned by Config.Interval when
	// IntervalSeconds is unset or non-positive.
	defaultFallbackInterval = 15 * time.Second
)

// Config is the full on-disk schema consumed by mon-agent. Field names match
// the YAML tags; unset values fall back to Default() and may be overlaid by
// MON_* environment variables in Load.
type Config struct {
	// ServerURL is the mon-server base URL (must start with http:// or
	// https://). Required; may be supplied via MON_SERVER_URL.
	ServerURL string `yaml:"server_url"`
	// IntervalSeconds is the collect/push cadence in seconds. Use the
	// Interval() helper to read it as a time.Duration.
	IntervalSeconds int `yaml:"interval_seconds"`
	// BufferDir is the on-disk spool directory used when the server is
	// unreachable. May be overridden by MON_BUFFER_DIR.
	BufferDir string `yaml:"buffer_dir"`
	// BufferMaxMB caps spool size in mebibytes. Beyond this, the oldest
	// batches are dropped.
	BufferMaxMB int `yaml:"buffer_max_mb"`
	// KeyFile is the path to the persisted agent credential
	// (agent_id:agent_key). May be overridden by MON_KEY_FILE.
	KeyFile string `yaml:"key_file"`
	// Labels are static key/value tags attached to every inventory snapshot
	// (e.g. environment=prod, region=eu-central). Server-side labels merge
	// in additively without overwriting these.
	Labels map[string]string `yaml:"labels"`
	// TLS controls how the agent verifies the server certificate.
	TLS TLSConfig `yaml:"tls"`
	// Proxmox enables the Proxmox VE inventory collector against a local
	// node's API.
	Proxmox ProxmoxConfig `yaml:"proxmox"`
	// DockerEndpoint is the Docker daemon socket / TCP endpoint the
	// workload collector probes. Empty disables the probe.
	DockerEndpoint string `yaml:"docker_endpoint"`
	// Packages controls the OS package inventory and update collector.
	Packages PackagesConfig `yaml:"packages"`
	// Redact controls agent-side PII filtering before payloads leave the
	// host.
	Redact RedactConfig `yaml:"redact"`
	// AutoUpdate opts the agent in or out of the timer-driven self-updater.
	AutoUpdate AutoUpdateConfig `yaml:"auto_update"`
	// Mail configures the mail-stack collector (Rspamd statistics endpoint).
	Mail MailConfig `yaml:"mail"`
}

// AutoUpdateConfig opts an agent in or out of the timer-driven self-updater.
// Default Enabled=true so freshly installed agents pick up patches without
// operator intervention; locked-down fleets can flip to false in the YAML.
type AutoUpdateConfig struct {
	// Enabled is a tri-state pointer: nil means "default on", explicit
	// false opts out, explicit true is a no-op affirmation.
	Enabled *bool `yaml:"enabled"`
}

// AutoUpdateEnabled returns true unless the operator explicitly set false.
func (c Config) AutoUpdateEnabled() bool {
	if c.AutoUpdate.Enabled == nil {
		return true
	}
	return *c.AutoUpdate.Enabled
}

// MailConfig enables and configures the mail-stack collector. Default
// Enabled=true so hosts with a mail stack are monitored without extra
// operator steps; operators can opt out by setting enabled: false.
type MailConfig struct {
	// Enabled is a tri-state pointer: nil means "default on", explicit
	// false opts out, explicit true is a no-op affirmation.
	Enabled *bool `yaml:"enabled"`
	// RspamdStatURL is the Rspamd HTTP statistics endpoint. Defaults to
	// defaultRspamdStatURL when unset.
	RspamdStatURL string `yaml:"rspamd_stat_url"`
}

// MailEnabled returns true unless the operator explicitly set false.
func (c Config) MailEnabled() bool {
	if c.Mail.Enabled == nil {
		return true
	}
	return *c.Mail.Enabled
}

// RspamdStatURL returns the configured Rspamd statistics URL or the default.
func (c Config) RspamdStatURL() string {
	if c.Mail.RspamdStatURL == "" {
		return defaultRspamdStatURL
	}
	return c.Mail.RspamdStatURL
}

// RedactConfig controls agent-side PII filtering applied before payloads ever
// leave the host. This is defence-in-depth: the server also redacts on
// ingest, but operators in regulated environments may want sensitive fields
// scrubbed at the source so they never traverse the wire. All toggles default
// off so existing deployments see no behaviour change.
type RedactConfig struct {
	// Enabled is the master switch; the per-field toggles below only take
	// effect when this is true.
	Enabled bool `yaml:"enabled"`
	// Shells, when true, masks shell paths in observed-user records.
	Shells bool `yaml:"shells"`
	// Homes, when true, masks home directories in observed-user records.
	Homes bool `yaml:"homes"`
	// SourceIPs, when true, hashes source_ip values in login events
	// (sha256, first 8 hex chars).
	SourceIPs bool `yaml:"source_ips"`
}

// TLSConfig configures how the agent trusts the mon-server certificate. If
// CAPinSHA256 is set, the leaf certificate's sha256 must match exactly. If
// CAFile is set, the PEM bundle is used as the trust root.
type TLSConfig struct {
	// CAPinSHA256 is a 64-char lowercase hex sha256 of the expected server
	// leaf certificate. Highest-trust verification mode.
	CAPinSHA256 string `yaml:"ca_pin_sha256"`
	// CAFile is the PEM bundle used as the trust root when CAPinSHA256 is
	// empty.
	CAFile string `yaml:"ca_file"`
	// Insecure disables TLS verification entirely. Dev-only escape hatch.
	Insecure bool `yaml:"insecure"`
}

// ProxmoxConfig enables and configures the Proxmox VE inventory collector
// against the local node's HTTPS API.
type ProxmoxConfig struct {
	// Enabled toggles the Proxmox collector on a per-host basis.
	Enabled bool `yaml:"enabled"`
	// APIURL is the Proxmox API base URL (e.g. https://127.0.0.1:8006).
	APIURL string `yaml:"api_url"`
	// APITokenID is the Proxmox token identifier
	// (e.g. user@pam!tokenname).
	APITokenID string `yaml:"api_token_id"`
	// APITokenSecretFile points at a 0600 file containing the token
	// secret. The agent reads it at startup rather than embedding the
	// secret in this YAML.
	APITokenSecretFile string `yaml:"api_token_secret_file"`
}

// PackagesConfig tunes the OS package inventory and pending-update collector.
type PackagesConfig struct {
	// Enabled toggles the entire collector. When false, no package data is
	// reported.
	Enabled bool `yaml:"enabled"`
	// UpdateCheckInterval bounds how often the agent invokes the package
	// manager to check for upgradable packages. Defaults to 30m.
	UpdateCheckInterval time.Duration `yaml:"update_check_interval"`
	// FullSnapshotMaxInterval caps how stale the full installed-package
	// list may get. Defaults to 24h.
	FullSnapshotMaxInterval time.Duration `yaml:"full_snapshot_max_interval"`
}

// Default returns a Config populated with Linux defaults. Callers typically
// pass the result to yaml.Unmarshal as the destination so unset fields keep
// these defaults.
func Default() Config {
	return Config{
		IntervalSeconds: defaultIntervalSeconds,
		BufferDir:       defaultBufferDir,
		BufferMaxMB:     defaultBufferMaxMB,
		KeyFile:         defaultKeyFile,
		Packages: PackagesConfig{
			Enabled:                 true,
			UpdateCheckInterval:     defaultPackageUpdateCheckInterval,
			FullSnapshotMaxInterval: defaultPackageFullSnapshotMaxInterval,
		},
	}
}

// Load reads YAML from path (if non-empty) and overlays select environment
// variables (MON_SERVER_URL, MON_KEY_FILE, MON_BUFFER_DIR). A missing config
// file is OK if defaults plus env suffice; a malformed file is fatal.
func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(b, &cfg); err != nil {
				return cfg, fmt.Errorf("parse %s: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// fall through, env may provide everything
		default:
			return cfg, fmt.Errorf("read %s: %w", path, err)
		}
	}

	if v := os.Getenv("MON_SERVER_URL"); v != "" {
		cfg.ServerURL = v
	}
	if v := os.Getenv("MON_KEY_FILE"); v != "" {
		cfg.KeyFile = v
	}
	if v := os.Getenv("MON_BUFFER_DIR"); v != "" {
		cfg.BufferDir = v
	}

	if cfg.ServerURL == "" {
		return cfg, errors.New("server_url required (config or MON_SERVER_URL)")
	}
	if !strings.HasPrefix(cfg.ServerURL, "https://") && !strings.HasPrefix(cfg.ServerURL, "http://") {
		return cfg, errors.New("server_url must start with http(s)://")
	}
	return cfg, nil
}

// Interval returns the collect/push cadence as a time.Duration, falling back
// to 15s when IntervalSeconds is unset or non-positive.
func (c Config) Interval() time.Duration {
	if c.IntervalSeconds <= 0 {
		return defaultFallbackInterval
	}
	return time.Duration(c.IntervalSeconds) * time.Second
}
