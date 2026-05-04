package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ServerURL          string            `yaml:"server_url"`
	IntervalSeconds    int               `yaml:"interval_seconds"`
	BufferDir          string            `yaml:"buffer_dir"`
	BufferMaxMB        int               `yaml:"buffer_max_mb"`
	KeyFile            string            `yaml:"key_file"`
	Labels             map[string]string `yaml:"labels"`
	TLS                TLSConfig         `yaml:"tls"`
	Proxmox            ProxmoxConfig     `yaml:"proxmox"`
	DockerEndpoint     string            `yaml:"docker_endpoint"`
	Packages           PackagesConfig    `yaml:"packages"`
	Redact             RedactConfig      `yaml:"redact"`
	AutoUpdate         AutoUpdateConfig  `yaml:"auto_update"`
}

// AutoUpdateConfig opts an agent in or out of the timer-driven self-updater.
// Default Enabled=true so freshly installed agents pick up patches without
// operator intervention; locked-down fleets can flip to false in the YAML.
type AutoUpdateConfig struct {
	Enabled *bool `yaml:"enabled"`
}

// AutoUpdateEnabled returns true unless the operator explicitly set false.
func (c Config) AutoUpdateEnabled() bool {
	if c.AutoUpdate.Enabled == nil {
		return true
	}
	return *c.AutoUpdate.Enabled
}

// RedactConfig controls agent-side PII filtering applied before payloads ever
// leave the host. This is defence-in-depth: the server also redacts on
// ingest, but operators in regulated environments may want sensitive fields
// scrubbed at the source so they never traverse the wire. All toggles default
// off so existing deployments see no behaviour change.
type RedactConfig struct {
	Enabled   bool `yaml:"enabled"`
	Shells    bool `yaml:"shells"`     // mask shell paths in observed users
	Homes     bool `yaml:"homes"`      // mask home directories
	SourceIPs bool `yaml:"source_ips"` // hash source_ip in login events (sha256, first 8 hex)
}

type TLSConfig struct {
	CAPinSHA256 string `yaml:"ca_pin_sha256"`
	CAFile      string `yaml:"ca_file"`
	Insecure    bool   `yaml:"insecure"`
}

type ProxmoxConfig struct {
	Enabled            bool   `yaml:"enabled"`
	APIURL             string `yaml:"api_url"`
	APITokenID         string `yaml:"api_token_id"`
	APITokenSecretFile string `yaml:"api_token_secret_file"`
}

type PackagesConfig struct {
	Enabled                 bool          `yaml:"enabled"`
	UpdateCheckInterval     time.Duration `yaml:"update_check_interval"`
	FullSnapshotMaxInterval time.Duration `yaml:"full_snapshot_max_interval"`
}

func Default() Config {
	return Config{
		IntervalSeconds: 15,
		BufferDir:       "/var/lib/mon-agent/spool",
		BufferMaxMB:     256,
		KeyFile:         "/etc/mon-agent/agent.key",
		Packages: PackagesConfig{
			Enabled:                 true,
			UpdateCheckInterval:     30 * time.Minute,
			FullSnapshotMaxInterval: 24 * time.Hour,
		},
	}
}

// Load reads YAML from path (if non-empty) and overlays select environment
// variables. Missing config file is OK if defaults + env suffice.
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

func (c Config) Interval() time.Duration {
	if c.IntervalSeconds <= 0 {
		return 15 * time.Second
	}
	return time.Duration(c.IntervalSeconds) * time.Second
}
