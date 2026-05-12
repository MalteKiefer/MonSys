//go:build linux

// Package updater downloads, cryptographically verifies, and atomically
// replaces the running mon-agent binary, then asks systemd to restart the
// service.
//
// # Scope and invocation model
//
// This package is intended to be invoked from a separate root-context
// systemd service (mon-agent-update.service) on a timer — never from the
// long-running agent process, which lacks the privileges to write
// /usr/local/bin or to call systemctl. Run is safe to call repeatedly: it
// short-circuits when the running version already matches the published
// one, and it refuses downgrades.
//
// # Linux-only
//
// The file carries a `//go:build linux` build tag. Two behaviours pin it to
// Linux:
//
//  1. The default RestartCmd is "systemctl try-restart mon-agent.service".
//     systemd is Linux-specific; other init systems (launchd, runit, SMF,
//     Windows SCM) need a different command surface and a different rollback
//     strategy.
//  2. The atomic swap relies on the kernel's "rename(2) over a running
//     executable is fine because the kernel keeps the old inode alive via
//     exec mmap until the process exits" property. Windows would refuse the
//     rename outright (sharing-violation on the in-use .exe) and would need
//     a MoveFileEx + reboot pending or a side-by-side install strategy.
//
// Porting to a non-Linux platform therefore requires more than dropping the
// build tag — it needs a platform-specific swap implementation. See Run's
// godoc for the cross-link.
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

// EnvInsecureOverride is the name of the environment variable that lets an
// operator bypass minisign verification when recovering from a key-loss
// event. Setting it to "1" causes the updater to proceed with sha256-only
// verification (the legacy AUDIT-401 path) and emit a loud slog.Error every
// time. Any other value, including unset, keeps signature verification
// enforced.
const EnvInsecureOverride = "MON_AGENT_UPDATE_INSECURE"

// Tunable constants. These were magic numbers scattered through Run; pulling
// them up makes it easy to audit security-relevant limits in one place.
const (
	// DefaultOverallTimeout caps the wall-clock duration of a single Run()
	// call when Options.Timeout is zero.
	DefaultOverallTimeout = 5 * time.Minute

	// DefaultHTTPTimeout is the per-request timeout applied to the
	// constructed http.Client when Options.HTTPClient is nil.
	DefaultHTTPTimeout = 60 * time.Second

	// MaxBinaryBytes caps the body of the downloaded agent binary. mon-agent
	// itself is <30 MiB today; 256 MiB leaves plenty of headroom for symbol
	// tables and debug builds while bounding a hostile server's ability to
	// fill the staging filesystem.
	MaxBinaryBytes = 256 << 20

	// MaxSignatureBytes caps the body of the downloaded .minisig file. Real
	// minisign signatures are ~150 bytes; 1 MiB is purely paranoia against
	// an attacker-controlled redirect.
	MaxSignatureBytes = 1 << 20

	// MaxManifestBytes caps the JSON manifest body to prevent the server
	// from blowing up the agent's memory.
	MaxManifestBytes = 64 << 10

	// MaxErrorBodyBytes is the cap when reading non-2xx response bodies for
	// inclusion in error messages.
	MaxErrorBodyBytes = 512

	// StagedBinaryMode is the file mode written onto the staged binary
	// BEFORE the atomic rename — anything else would land an unreadable or
	// non-executable file at /usr/local/bin/mon-agent.
	StagedBinaryMode os.FileMode = 0o755

	// StagingDirMode is the permission set on the staging directory if Run
	// has to create it.
	StagingDirMode os.FileMode = 0o700

	// StagedDownloadMode is the temporary mode used while we're still
	// writing into the staged file; we chmod up to StagedBinaryMode just
	// before the swap.
	StagedDownloadMode os.FileMode = 0o600
)

// componentLogger is the package-scoped slog handle with the
// component=updater attribute pre-bound, used by every emitted log line.
//
//nolint:gochecknoglobals // canonical slog-with-component idiom
var componentLogger = slog.With("component", "updater")

// Manifest mirrors the server's agentupdate.Manifest. It is re-declared here
// to avoid a server→agent import cycle.
type Manifest struct {
	Version   string                 `json:"version"`
	Channel   string                 `json:"channel"`
	CheckedAt time.Time              `json:"checked_at"`
	Binaries  map[string]ManifestBin `json:"binaries"`
	Source    string                 `json:"source"`
}

// ManifestBin describes a single platform-specific binary in the manifest:
// where to fetch it, the SHA-256 of its content, and (optionally) an
// override URL for the detached minisign signature.
type ManifestBin struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	// MinisigURL points at the detached minisign signature that covers the
	// binary at URL. When empty, the updater derives it as URL + ".minisig",
	// matching the convention used by deploy/release.yaml.
	MinisigURL string `json:"minisig_url,omitempty"`
}

// Result reports the outcome of an update attempt for telemetry/logging.
// Callers use it to decide what to record in the agent's metrics stream.
type Result struct {
	// From is the version the agent was running at entry to Run.
	From string
	// To is the version the manifest advertised. On a successful rollback
	// this is rewritten back to From.
	To string
	// BinaryPath is the absolute path the updater targeted; echoed for
	// diagnostics so the journal entry is self-contained.
	BinaryPath string
	// Replaced is true only when the final binary at BinaryPath is the
	// newly-downloaded one (so when a rollback succeeds, this is false).
	Replaced bool
}

// Options is the input bundle Run consumes. ServerURL and BinaryPath are
// mandatory; the rest carry sensible defaults.
type Options struct {
	// ServerURL is the mon-server base, e.g. https://mon.example.com.
	ServerURL string
	// CurrentVersion is the version.Version of the *running* mon-agent.
	// Empty means "unknown" and disables the downgrade-protection check.
	CurrentVersion string
	// BinaryPath is the absolute path to install the new binary at, e.g.
	// /usr/local/bin/mon-agent.
	BinaryPath string
	// StagingDir is where the temp binary and signature are written. It
	// should live on the same filesystem as BinaryPath's directory so the
	// rename is atomic; the cross-filesystem fallback (copyReplace) is
	// engaged transparently when it is not.
	StagingDir string
	// HTTPClient is the client used for manifest, binary, and signature
	// fetches. nil means a fresh client with DefaultHTTPTimeout.
	HTTPClient *http.Client
	// RestartCmd is the command exec'd after a successful swap, typically
	// ["systemctl", "try-restart", "mon-agent.service"]. Empty means no
	// restart attempt — the new binary will be picked up on the next agent
	// crash or reboot.
	RestartCmd []string
	// Timeout is the overall wall-clock cap for one Run call.
	// Zero means DefaultOverallTimeout.
	Timeout time.Duration
}

// Run performs one self-update tick.
//
// It returns (Result{Replaced:false}, nil) when the manifest advertises a
// version equal to or older than the current one, when there is no platform
// entry, or when the manifest is empty. All other no-op outcomes are
// expressed as wrapped errors.
//
// On a successful swap with a configured RestartCmd, Run also calls
// systemctl try-restart. If that fails it best-effort rolls back to the
// previous binary snapshot and re-runs the restart, returning a non-nil
// error in either case so the journal records the failure.
//
// Linux-specific: see the package godoc — the rename-over-running-exe
// strategy is a Linux property; do not port this file without auditing the
// swap path for the target OS.
func Run(ctx context.Context, o Options) (Result, error) {
	res := Result{From: o.CurrentVersion, BinaryPath: o.BinaryPath}

	if o.Timeout <= 0 {
		o.Timeout = DefaultOverallTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: DefaultHTTPTimeout}
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
		componentLogger.Warn("self-update: downgrade refused",
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
	if err := os.MkdirAll(filepath.Dir(tmpPath), StagingDirMode); err != nil {
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
				return res, fmt.Errorf("%w; force-refresh failed: %w", err, mErr)
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
	insecure := os.Getenv(EnvInsecureOverride) == "1"
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
		componentLogger.Error("self-update: signature download failed but MON_AGENT_UPDATE_INSECURE=1; proceeding with sha256-only verification",
			"url", sigURL, "err", sigErr)
	} else {
		ok, vErr := verifyMinisig(tmpPath, sigTmpPath, PublicKey)
		_ = os.Remove(sigTmpPath)
		if !ok {
			if insecure {
				componentLogger.Error("self-update: minisign verification FAILED but MON_AGENT_UPDATE_INSECURE=1; proceeding anyway",
					"err", vErr)
			} else {
				_ = os.Remove(tmpPath)
				return res, fmt.Errorf("minisign verify: %w", vErr)
			}
		}
	}

	// Chmod must happen BEFORE the rename: once the file lives at
	// finalPath, an os.Chmod on it would briefly leave the running service
	// pointing at a 0600 binary if anything looks at the executable bit
	// during install.
	if err := os.Chmod(tmpPath, StagedBinaryMode); err != nil {
		_ = os.Remove(tmpPath)
		return res, fmt.Errorf("chmod staged binary: %w", err)
	}

	// Log the SHA-256 of the now-final-perms staged binary just before the
	// swap. This is purely an audit breadcrumb — minisign already provides
	// authentication; this lets us correlate "what bytes did we install at
	// 10:34:02?" against the manifest after the fact.
	if hash, hErr := hashFile(tmpPath); hErr == nil {
		componentLogger.Info("self-update: staged binary ready for swap",
			"path", tmpPath,
			"sha256", hash,
			"version", res.To,
			"size_bytes", fileSize(tmpPath))
	}

	finalPath := o.BinaryPath
	prevPath := finalPath + ".prev"
	// AUDIT-403: snapshot the running binary before swapping. Best-effort —
	// a fresh install has no existing finalPath, in which case there is
	// nothing to roll back to and we skip silently.
	snapshotted := false
	if _, statErr := os.Stat(finalPath); statErr == nil {
		if cpErr := copyFile(finalPath, prevPath, StagedBinaryMode); cpErr != nil {
			componentLogger.Warn("self-update: snapshot of previous binary failed; proceeding without rollback safety net",
				"path", prevPath, "err", cpErr)
		} else {
			snapshotted = true
		}
	}

	// Atomic swap. os.Rename is the canonical Linux atomic-replace primitive
	// — the rename(2) syscall guarantees that any concurrent open() of
	// finalPath sees either the old or the new inode, never a partial
	// write. Critically, on Linux the kernel keeps the old inode alive
	// through exec_mmap as long as the running process holds it, so
	// renaming over the executable of a live mon-agent does NOT corrupt
	// the running process — it just means the next exec/restart picks up
	// the new file.
	//
	// On a cross-filesystem path (EXDEV — common when /var/lib is tmpfs and
	// /usr/local/bin is the root fs), os.Rename returns *LinkError and we
	// fall back to copyReplace, which writes-then-renames inside the
	// destination directory so the final step is still rename(2).
	if err := os.Rename(tmpPath, finalPath); err != nil {
		if cpErr := copyReplace(tmpPath, finalPath); cpErr != nil {
			_ = os.Remove(tmpPath)
			return res, fmt.Errorf("install binary: rename=%w copy=%w", err, cpErr)
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
			// audit 2026-05-12 §4.3.7: mirror the forward-path EXDEV
			// fallback. If prevPath and finalPath are on different
			// filesystems (rename(2) returns *LinkError with EXDEV), fall
			// back to copyReplace so rollback completes the same way the
			// initial swap did.
			rbErr := os.Rename(prevPath, finalPath)
			if rbErr != nil {
				if cpErr := copyReplace(prevPath, finalPath); cpErr != nil {
					return res, fmt.Errorf("restart %v: %w (output: %s); rollback failed: rename=%w copy=%w",
						o.RestartCmd, err, strings.TrimSpace(string(out)), rbErr, cpErr)
				}
				_ = os.Remove(prevPath)
			}
			res.Replaced = false
			res.To = res.From
			c2 := exec.CommandContext(ctx, o.RestartCmd[0], o.RestartCmd[1:]...) //nolint:gosec // operator-supplied
			if rOut, rErr := c2.CombinedOutput(); rErr != nil {
				return res, fmt.Errorf("restart %v failed: %w (output: %s); rolled back to previous binary but post-rollback restart also failed: %w (output: %s)",
					o.RestartCmd, err, strings.TrimSpace(string(out)), rErr, strings.TrimSpace(string(rOut)))
			}
			// Rollback succeeded; the snapshot file has been consumed by
			// the rename/copy step above. Nothing to clean up here.
			return res, fmt.Errorf("restart %v failed: %w (output: %s); rolled back to previous binary at %s and restarted successfully",
				o.RestartCmd, err, strings.TrimSpace(string(out)), prevPath)
		}
	}
	// audit 2026-05-12 §4.3.6: on the happy path the .prev snapshot is no
	// longer needed — the new binary is live and systemd has acknowledged
	// the restart (or no RestartCmd was configured at all, in which case
	// nothing will ever roll back from the previous tick's snapshot
	// either). Remove it so /usr/local/bin doesn't accumulate orphans.
	// Best-effort: a leftover .prev is not a failure.
	if snapshotted {
		_ = os.Remove(prevPath)
	}
	return res, nil
}

// fetchManifest GETs /v1/agents/latest-version on the configured server,
// honouring `?fresh=1` when force is true (used after a sha256 mismatch).
func fetchManifest(ctx context.Context, cli *http.Client, base string, force bool) (*Manifest, error) {
	url := strings.TrimRight(base, "/") + "/v1/agents/latest-version"
	if force {
		url += "?fresh=1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBodyBytes))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, MaxManifestBytes)).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// downloadAndVerify streams the binary at url into dst, capping at
// MaxBinaryBytes, and confirms the sha256 of the received bytes matches
// wantHex. The on-disk file is fsynced before close so the subsequent
// rename is durable.
func downloadAndVerify(ctx context.Context, cli *http.Client, url, wantHex, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, StagedDownloadMode)
	if err != nil {
		return err
	}
	h := sha256.New()
	mw := io.MultiWriter(f, h)
	n, err := io.CopyN(mw, resp.Body, MaxBinaryBytes)
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

// downloadFile fetches url to dst, capping the body at MaxSignatureBytes.
// It is used for the .minisig signature blob, which is ~120 bytes — the cap
// is purely a safety measure against an attacker-controlled redirect
// handing back a huge payload that might exhaust staging-dir space.
func downloadFile(ctx context.Context, cli *http.Client, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, StagedDownloadMode)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(f, resp.Body, MaxSignatureBytes); err != nil && !errors.Is(err, io.EOF) {
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
// fsyncs, then renames so the final visible step is still rename(2) — the
// same atomic-swap guarantee as the in-place rename path.
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
	if err := os.Chmod(tmp.Name(), StagedBinaryMode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// isSHAMismatch matches the error string downloadAndVerify produces when
// the fetched binary's hash disagrees with the manifest. We compare on
// substring rather than introducing a sentinel error so the existing detail
// (want/got hashes) stays visible in the wrapped %w chain.
func isSHAMismatch(err error) bool {
	return err != nil && strings.Contains(err.Error(), "sha256 mismatch")
}

// normalizeVersion strips a leading "v" so "v0.1.5" matches "0.1.5". It
// returns the input untouched on empty.
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

// hashFile returns the hex-encoded SHA-256 of the file at path. It is used
// purely for the pre-swap audit-log breadcrumb; minisign provides the real
// authentication.
func hashFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // operator-controlled path
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fileSize returns the byte size of path, or -1 on stat error. Used only
// for log context so the error is intentionally swallowed.
func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return st.Size()
}
