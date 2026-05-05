// Package updater downloads, verifies, and atomically replaces the running
// mon-agent binary, then triggers systemd to restart the service.
//
// It is intended to be invoked from a separate root-context systemd service
// (mon-agent-update.service) on a timer — never from the long-running agent
// process, which lacks the privileges to write /usr/local/bin or call
// systemctl. The function is safe to call repeatedly: it short-circuits when
// the running version already matches the published one.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Manifest mirrors the server's agentupdate.Manifest. Re-declared here to
// avoid a server→agent import cycle.
type Manifest struct {
	Version   string                  `json:"version"`
	Channel   string                  `json:"channel"`
	CheckedAt time.Time               `json:"checked_at"`
	Binaries  map[string]ManifestBin  `json:"binaries"`
	Source    string                  `json:"source"`
}

type ManifestBin struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Result reports the outcome of an update attempt for telemetry/logging.
type Result struct {
	From       string
	To         string
	BinaryPath string
	Replaced   bool
}

type Options struct {
	ServerURL      string        // mon-server base, e.g. https://mon.example.com
	CurrentVersion string        // version.Version of the *running* mon-agent
	BinaryPath     string        // /usr/local/bin/mon-agent
	StagingDir     string        // /var/lib/mon-agent (must be on the same fs as BinaryPath's directory in the strict case; we cope with tmpfs by falling back)
	HTTPClient     *http.Client
	RestartCmd     []string      // typically ["systemctl", "try-restart", "mon-agent.service"]
	Timeout        time.Duration // overall wall-clock cap; default 5m
}

// Run performs one self-update tick. Returns nil + Replaced=false when no
// update was needed.
func Run(ctx context.Context, o Options) (Result, error) {
	res := Result{From: o.CurrentVersion, BinaryPath: o.BinaryPath}

	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if o.ServerURL == "" || o.BinaryPath == "" {
		return res, errors.New("updater: ServerURL and BinaryPath are required")
	}

	m, err := fetchManifest(ctx, o.HTTPClient, o.ServerURL, false)
	if err != nil {
		return res, fmt.Errorf("manifest: %w", err)
	}
	res.To = m.Version

	if m.Version == "" {
		return res, errors.New("manifest returned empty version")
	}
	if normalizeVersion(m.Version) == normalizeVersion(o.CurrentVersion) {
		return res, nil
	}

	key := runtime.GOOS + "/" + runtime.GOARCH
	bin, ok := m.Binaries[key]
	if !ok || bin.URL == "" || bin.SHA256 == "" {
		return res, fmt.Errorf("manifest has no entry for %s", key)
	}

	tmpPath := filepath.Join(o.StagingDir, "mon-agent.new")
	if err := os.MkdirAll(filepath.Dir(tmpPath), 0o700); err != nil {
		return res, fmt.Errorf("staging dir: %w", err)
	}
	if err := downloadAndVerify(ctx, o.HTTPClient, bin.URL, bin.SHA256, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		// SHA mismatch is the canonical "server cache is stale" symptom: the
		// manifest's hash points at an asset that GitHub has since overwritten.
		// Ask the server to bypass its cache and try once more before failing.
		if isSHAMismatch(err) {
			m2, mErr := fetchManifest(ctx, o.HTTPClient, o.ServerURL, true)
			if mErr != nil {
				return res, fmt.Errorf("%w; force-refresh failed: %v", err, mErr)
			}
			bin2, ok2 := m2.Binaries[key]
			if !ok2 || bin2.URL == "" || bin2.SHA256 == "" {
				return res, fmt.Errorf("%w; refreshed manifest has no entry for %s", err, key)
			}
			res.To = m2.Version
			if err2 := downloadAndVerify(ctx, o.HTTPClient, bin2.URL, bin2.SHA256, tmpPath); err2 != nil {
				_ = os.Remove(tmpPath)
				return res, fmt.Errorf("post-refresh: %w", err2)
			}
		} else {
			return res, err
		}
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return res, fmt.Errorf("chmod staged binary: %w", err)
	}

	finalPath := o.BinaryPath
	// Atomic rename only works within the same filesystem. If it fails
	// (different fs / read-only fs), copy + replace.
	if err := os.Rename(tmpPath, finalPath); err != nil {
		if cpErr := copyReplace(tmpPath, finalPath); cpErr != nil {
			_ = os.Remove(tmpPath)
			return res, fmt.Errorf("install binary: rename=%w copy=%v", err, cpErr)
		}
		_ = os.Remove(tmpPath)
	}
	res.Replaced = true

	if len(o.RestartCmd) > 0 {
		// Best effort — log failure but don't undo the swap. systemd may
		// still pick up the new binary on its next scheduled restart.
		c := exec.CommandContext(ctx, o.RestartCmd[0], o.RestartCmd[1:]...)
		if out, err := c.CombinedOutput(); err != nil {
			return res, fmt.Errorf("restart %v: %w (output: %s)", o.RestartCmd, err, strings.TrimSpace(string(out)))
		}
	}
	return res, nil
}

func fetchManifest(ctx context.Context, cli *http.Client, base string, force bool) (*Manifest, error) {
	url := strings.TrimRight(base, "/") + "/v1/agents/latest-version"
	if force {
		url += "?fresh=1"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

func downloadAndVerify(ctx context.Context, cli *http.Client, url, wantHex, dst string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	mw := io.MultiWriter(f, h)
	const maxBytes = 256 << 20 // 256 MiB cap — agents are <30 MiB today
	n, err := io.CopyN(mw, resp.Body, maxBytes)
	if err != nil && !errors.Is(err, io.EOF) {
		_ = f.Close()
		return fmt.Errorf("write: %w", err)
	}
	if n == 0 {
		_ = f.Close()
		return errors.New("downloaded 0 bytes")
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, wantHex) {
		return fmt.Errorf("sha256 mismatch: want %s got %s", wantHex, got)
	}
	return nil
}

// copyReplace handles the cross-filesystem fallback when os.Rename rejects
// the swap (EXDEV). Writes through a temp file in the destination directory,
// fsyncs, then renames.
func copyReplace(src, dst string) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".mon-agent.swap-*")
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmp.Name())
	}()

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := io.Copy(tmp, in); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// isSHAMismatch matches the error string downloadAndVerify produces when the
// fetched binary's hash disagrees with the manifest. We compare on substring
// rather than introducing a sentinel error so the existing detail (want/got
// hashes) stays visible in the wrapped %w chain.
func isSHAMismatch(err error) bool {
	return err != nil && strings.Contains(err.Error(), "sha256 mismatch")
}

// normalizeVersion strips a leading "v" so "v0.1.5" matches "0.1.5". Returns
// the input untouched on empty.
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if v[0] == 'v' || v[0] == 'V' {
		return v[1:]
	}
	return v
}
