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
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// envInsecureOverride lets an operator bypass minisign verification when
// recovering from a key-loss event. Setting it to "1" causes the updater to
// proceed with sha256-only verification (the legacy AUDIT-401 path) and emit
// a loud slog.Error every time. Any other value, including unset, keeps
// signature verification enforced.
const envInsecureOverride = "MON_AGENT_UPDATE_INSECURE"

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
	// MinisigURL points at the detached minisign signature that covers the
	// binary at URL. When empty, the updater derives it as URL + ".minisig",
	// matching the convention used by deploy/release.yaml.
	MinisigURL string `json:"minisig_url,omitempty"`
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

	// AUDIT-402: downgrade protection. Refuse to install a published version
	// that is older than what is currently running. compareSemver knows how
	// to compare both real semvers ("v0.2.0") and rolling describe-style
	// pseudo-versions ("v0.1.5-23-gabcd").
	if o.CurrentVersion != "" && compareSemver(m.Version, o.CurrentVersion) <= 0 {
		slog.Warn("self-update: downgrade refused",
			"current", o.CurrentVersion,
			"manifest", m.Version,
			"channel", m.Channel)
		return res, nil
	}

	key := runtime.GOOS + "/" + runtime.GOARCH
	bin, ok := m.Binaries[key]
	if !ok || bin.URL == "" || bin.SHA256 == "" {
		return res, fmt.Errorf("manifest has no entry for %s", key)
	}

	tmpPath := filepath.Join(o.StagingDir, "mon-agent.new")
	sigTmpPath := tmpPath + ".minisig"
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
			bin = bin2
		} else {
			return res, err
		}
	}

	// AUDIT-401: cryptographic signature verification. Fetch the .minisig
	// file matched to the binary URL and verify it against the embedded
	// public key BEFORE the atomic rename. The MON_AGENT_UPDATE_INSECURE
	// escape hatch is the only way to skip this, and it logs loudly.
	insecure := os.Getenv(envInsecureOverride) == "1"
	sigURL := bin.MinisigURL
	if sigURL == "" {
		sigURL = bin.URL + ".minisig"
	}
	sigErr := downloadFile(ctx, o.HTTPClient, sigURL, sigTmpPath)
	if sigErr != nil {
		if !insecure {
			_ = os.Remove(tmpPath)
			_ = os.Remove(sigTmpPath)
			return res, fmt.Errorf("download signature %s: %w", sigURL, sigErr)
		}
		slog.Error("self-update: signature download failed but MON_AGENT_UPDATE_INSECURE=1; proceeding with sha256-only verification",
			"url", sigURL, "err", sigErr)
	} else {
		ok, vErr := verifyMinisig(tmpPath, sigTmpPath, PublicKey)
		_ = os.Remove(sigTmpPath)
		if !ok {
			if insecure {
				slog.Error("self-update: minisign verification FAILED but MON_AGENT_UPDATE_INSECURE=1; proceeding anyway",
					"err", vErr)
			} else {
				_ = os.Remove(tmpPath)
				return res, fmt.Errorf("minisign verify: %w", vErr)
			}
		}
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return res, fmt.Errorf("chmod staged binary: %w", err)
	}

	finalPath := o.BinaryPath
	prevPath := finalPath + ".prev"
	// AUDIT-403: snapshot the running binary before swapping. Best-effort —
	// a fresh install has no existing finalPath, in which case there is
	// nothing to roll back to and we skip silently.
	snapshotted := false
	if _, statErr := os.Stat(finalPath); statErr == nil {
		if cpErr := copyFile(finalPath, prevPath, 0o755); cpErr != nil {
			slog.Warn("self-update: snapshot of previous binary failed; proceeding without rollback safety net",
				"path", prevPath, "err", cpErr)
		} else {
			snapshotted = true
		}
	}

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
		c := exec.CommandContext(ctx, o.RestartCmd[0], o.RestartCmd[1:]...) //nolint:gosec // operator-supplied
		out, err := c.CombinedOutput()
		if err != nil {
			// AUDIT-403: rollback. Restore the snapshot (if we have one) and
			// kick the unit again so the previous binary is what ends up
			// running. We surface BOTH errors in the returned message so the
			// systemd journal shows the upgrade and the rollback.
			if !snapshotted {
				return res, fmt.Errorf("restart %v: %w (output: %s); no snapshot available, rollback skipped",
					o.RestartCmd, err, strings.TrimSpace(string(out)))
			}
			rbErr := os.Rename(prevPath, finalPath)
			if rbErr != nil {
				return res, fmt.Errorf("restart %v: %w (output: %s); rollback failed: %v",
					o.RestartCmd, err, strings.TrimSpace(string(out)), rbErr)
			}
			res.Replaced = false
			res.To = res.From
			c2 := exec.CommandContext(ctx, o.RestartCmd[0], o.RestartCmd[1:]...) //nolint:gosec // operator-supplied
			if rOut, rErr := c2.CombinedOutput(); rErr != nil {
				return res, fmt.Errorf("restart %v failed: %w (output: %s); rolled back to previous binary but post-rollback restart also failed: %v (output: %s)",
					o.RestartCmd, err, strings.TrimSpace(string(out)), rErr, strings.TrimSpace(string(rOut)))
			}
			return res, fmt.Errorf("restart %v failed: %w (output: %s); rolled back to previous binary at %s and restarted successfully",
				o.RestartCmd, err, strings.TrimSpace(string(out)), prevPath)
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

// downloadFile fetches url to dst, capping the body at 1 MiB. It is used for
// the .minisig signature blob, which is ~120 bytes — the cap is purely a
// safety measure against an attacker-controlled redirect handing back a huge
// payload that might exhaust staging-dir space.
func downloadFile(ctx context.Context, cli *http.Client, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	const maxSig = 1 << 20 // 1 MiB cap; real .minisig files are ~150B.
	if _, err := io.CopyN(f, resp.Body, maxSig); err != nil && !errors.Is(err, io.EOF) {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// copyFile duplicates src to dst with the given mode. Used to snapshot the
// running binary before the atomic swap so we can restore it on a failed
// systemctl try-restart (AUDIT-403).
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // operator-controlled path
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
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
