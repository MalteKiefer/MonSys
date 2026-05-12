//go:build linux

// Package packages collects installed-package state, pending updates, and
// repo-metadata freshness via the host's native package manager (dpkg, rpm,
// pacman, apk).
//
// All commands are read-only and run through safeexec so we never accidentally
// mutate package state and so the runtime is bounded.
//
// The package is Linux-only: it shells `dpkg-query`/`rpm`/`pacman`/`apk` and
// stats /var/lib/{apt,dnf,pacman,apk}, none of which exist on other platforms.
// Per-manager logic is split into companion files (packages_apt.go etc.) but
// all share this build tag.
package packages

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/MalteKiefer/MonSys/internal/agent/config"
	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// Manager identifies the active package manager.
type Manager string

// Supported package managers, in dispatch-priority order: dpkg is checked
// first because Debian/Ubuntu derivatives also commonly install rpm-tools.
const (
	ManagerDpkg   Manager = "dpkg"
	ManagerRPM    Manager = "rpm"
	ManagerPacman Manager = "pacman"
	ManagerAPK    Manager = "apk"
)

// Command names. Centralised so safeexec calls and Available() probes stay in
// sync if a binary ever moves.
const (
	cmdDpkgQuery = "dpkg-query"
	cmdRPM       = "rpm"
	cmdPacman    = "pacman"
	cmdAPK       = "apk"
	cmdAPT       = "apt"
	cmdDNF       = "dnf"
)

// Per-command timeouts. dnf is the slowest because `check-update` and
// `updateinfo list` both hit the network; the others operate on local
// metadata only.
const (
	timeoutInstalled    = 30 * time.Second
	timeoutAPTList      = 30 * time.Second
	timeoutDNFCheck     = 60 * time.Second
	timeoutPacmanUpd    = 20 * time.Second
	timeoutAPKVersion   = 20 * time.Second
	scannerMaxLineBytes = 1 << 20 // 1 MiB — dpkg lists can have very long Description lines
	scannerInitialBuf   = 64 * 1024
)

// ErrUnsupportedManager is returned by the internal dispatch when c.mgr was
// constructed with a value outside the four supported managers. In practice
// New() guards against this, but it surfaces clearly in tests.
var ErrUnsupportedManager = errors.New("unsupported package manager")

// Collector implements collector.Source.
//
// Inventory is intentionally NOT implemented: package state lives in
// PackageReport which already covers "current snapshot" semantics, and dpkg
// can produce 100k-line listings that would dwarf the rest of the inventory
// if we duplicated it there.
type Collector struct {
	cfg      config.PackagesConfig
	mgr      Manager
	log      *slog.Logger
	lastRun  time.Time
	lastHash string

	// run is pluggable for tests; defaults to safeexec.RunWithTimeout.
	run  func(ctx context.Context, d time.Duration, name string, args ...string) ([]byte, error)
	stat func(string) (os.FileInfo, error)
	now  func() time.Time
}

// New picks the first package manager available on the host. Returns
// (nil, false) if none is available — the agent should then skip the package
// collector entirely.
func New(cfg config.PackagesConfig) (*Collector, bool) {
	for _, m := range []Manager{ManagerDpkg, ManagerRPM, ManagerPacman, ManagerAPK} {
		if safeexec.Available(string(m)) {
			return &Collector{
				cfg:  cfg,
				mgr:  m,
				log:  slog.With("component", "packages", "manager", string(m)),
				run:  safeexec.RunWithTimeout,
				stat: os.Stat,
				now:  func() time.Time { return time.Now().UTC() },
			}, true
		}
	}
	return nil, false
}

// Name returns the collector identifier, including the active manager so logs
// disambiguate dpkg vs rpm hosts.
func (c *Collector) Name() string { return "packages-" + string(c.mgr) }

// Collect runs at the agent tick rate but is internally throttled by
// cfg.UpdateCheckInterval (refreshing the update list is the most expensive
// step). Installed packages are re-emitted only when the state hash changes.
func (c *Collector) Collect(ctx context.Context, batch *apitypes.IngestRequest) error {
	if !c.cfg.Enabled {
		return nil
	}
	now := c.now()
	if !c.lastRun.IsZero() && now.Sub(c.lastRun) < c.cfg.UpdateCheckInterval {
		return nil
	}
	c.lastRun = now

	report := apitypes.PackageReport{Time: now}

	installed, err := c.listInstalled(ctx)
	if err != nil {
		return fmt.Errorf("list installed: %w", err)
	}
	report.Summary.InstalledCount = len(installed)

	hash := stateHash(installed)
	if hash != c.lastHash {
		report.Installed = installed
		c.lastHash = hash
	}
	report.StateHash = hash

	updates, err := c.listUpdates(ctx)
	if err != nil {
		// Non-fatal: a flaky network or rate-limited mirror shouldn't drop
		// the installed snapshot. We just leave updates empty.
		c.log.Debug("list updates failed", "err", err)
		updates = nil
	}
	report.Updates = updates
	report.Summary.UpdatesCount = len(updates)
	for _, u := range updates {
		if u.IsSecurity {
			report.Summary.SecurityUpdates++
		}
	}

	if rs, ok := c.repoState(); ok {
		report.RepoStates = []apitypes.RepoMetaState{rs}
		report.Summary.MetadataAgeSec = rs.MetadataAgeSec
	}

	batch.Packages = &report
	return nil
}

// listInstalled dispatches to the per-manager installed-package reader.
func (c *Collector) listInstalled(ctx context.Context) ([]apitypes.InstalledPackage, error) {
	switch c.mgr {
	case ManagerDpkg:
		return c.installedDpkg(ctx)
	case ManagerRPM:
		return c.installedRPM(ctx)
	case ManagerPacman:
		return c.installedPacman(ctx)
	case ManagerAPK:
		return c.installedAPK(ctx)
	}
	return nil, fmt.Errorf("%w: %q", ErrUnsupportedManager, c.mgr)
}

// listUpdates dispatches to the per-manager pending-update reader. Managers
// without a known update mechanism return (nil, nil) rather than an error so
// Collect() can still emit the installed snapshot.
func (c *Collector) listUpdates(ctx context.Context) ([]apitypes.PendingUpdate, error) {
	switch c.mgr {
	case ManagerPacman:
		return c.updatesPacman(ctx)
	case ManagerDpkg:
		return c.updatesAPT(ctx)
	case ManagerRPM:
		return c.updatesDNF(ctx)
	case ManagerAPK:
		return c.updatesAPK(ctx)
	}
	return nil, nil
}

// repoState reports the mtime of the manager's metadata cache directory.
// Returns ok=false when the path is missing or unreadable — that's a legitimate
// "no signal" state, not an error worth surfacing.
func (c *Collector) repoState() (apitypes.RepoMetaState, bool) {
	var path string
	switch c.mgr {
	case ManagerDpkg:
		path = "/var/lib/apt/lists"
	case ManagerRPM:
		path = "/var/cache/dnf"
	case ManagerPacman:
		path = "/var/lib/pacman/sync"
	case ManagerAPK:
		path = "/var/cache/apk"
	default:
		return apitypes.RepoMetaState{}, false
	}
	fi, err := c.stat(path)
	if err != nil {
		return apitypes.RepoMetaState{}, false
	}
	mtime := fi.ModTime().UTC()
	return apitypes.RepoMetaState{
		Manager:        string(c.mgr),
		MetadataMtime:  mtime,
		MetadataAgeSec: int64(c.now().Sub(mtime).Seconds()),
	}, true
}

// parseTSV scans tab-separated output and builds an InstalledPackage per row.
// Rows with fewer than expectedFields columns are skipped silently — they're
// typically blank lines or warnings that some package tools print to stdout.
func parseTSV(out []byte, expectedFields int, build func([]string) apitypes.InstalledPackage) []apitypes.InstalledPackage {
	var pkgs []apitypes.InstalledPackage
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, scannerInitialBuf), scannerMaxLineBytes)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < expectedFields {
			continue
		}
		pkgs = append(pkgs, build(f))
	}
	return pkgs
}

// stateHash produces a stable SHA-256 over the installed-package set. The
// caller uses it to skip re-emitting the full list when nothing changed
// between collection ticks.
func stateHash(installed []apitypes.InstalledPackage) string {
	cpy := make([]apitypes.InstalledPackage, len(installed))
	copy(cpy, installed)
	sort.Slice(cpy, func(i, j int) bool {
		if cpy[i].Manager != cpy[j].Manager {
			return cpy[i].Manager < cpy[j].Manager
		}
		if cpy[i].Name != cpy[j].Name {
			return cpy[i].Name < cpy[j].Name
		}
		if cpy[i].Version != cpy[j].Version {
			return cpy[i].Version < cpy[j].Version
		}
		return cpy[i].Arch < cpy[j].Arch
	})
	h := sha256.New()
	for _, p := range cpy {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\n", p.Manager, p.Name, p.Version, p.Arch)
	}
	return hex.EncodeToString(h.Sum(nil))
}
