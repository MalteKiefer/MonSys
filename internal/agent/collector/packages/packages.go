// Package packages collects installed-package state, pending updates, and
// repo-metadata freshness via the host's native package manager (dpkg, rpm,
// pacman, apk). All commands are read-only and run through safeexec so we
// never accidentally mutate package state and so the runtime is bounded.
package packages

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

const (
	ManagerDpkg   Manager = "dpkg"
	ManagerRPM    Manager = "rpm"
	ManagerPacman Manager = "pacman"
	ManagerAPK    Manager = "apk"
)

// Collector implements collector.Source. Inventory is intentionally NOT
// implemented: package state lives in PackageReport which already covers
// "current snapshot" semantics, and dpkg can produce 100k-line listings that
// would dwarf the rest of the inventory if we duplicated it there.
type Collector struct {
	cfg     config.PackagesConfig
	mgr     Manager
	lastRun time.Time
	lastHash string

	// pluggable for tests; defaults to safeexec.RunWithTimeout
	run    func(ctx context.Context, d time.Duration, name string, args ...string) ([]byte, error)
	stat   func(string) (os.FileInfo, error)
	now    func() time.Time
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
				run:  safeexec.RunWithTimeout,
				stat: os.Stat,
				now:  func() time.Time { return time.Now().UTC() },
			}, true
		}
	}
	return nil, false
}

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

// --- Installed lists -------------------------------------------------------

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
	return nil, fmt.Errorf("unsupported manager %q", c.mgr)
}

func (c *Collector) installedDpkg(ctx context.Context) ([]apitypes.InstalledPackage, error) {
	out, err := c.run(ctx, 30*time.Second, "dpkg-query", "-W",
		`-f=${binary:Package}\t${Version}\t${Architecture}\n`)
	if err != nil {
		return nil, err
	}
	return parseTSV(out, ManagerDpkg, 3, func(f []string) apitypes.InstalledPackage {
		return apitypes.InstalledPackage{Manager: string(ManagerDpkg), Name: f[0], Version: f[1], Arch: f[2]}
	}), nil
}

func (c *Collector) installedRPM(ctx context.Context) ([]apitypes.InstalledPackage, error) {
	out, err := c.run(ctx, 30*time.Second, "rpm", "-qa",
		"--queryformat", "%{NAME}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n")
	if err != nil {
		return nil, err
	}
	return parseTSV(out, ManagerRPM, 3, func(f []string) apitypes.InstalledPackage {
		return apitypes.InstalledPackage{Manager: string(ManagerRPM), Name: f[0], Version: f[1], Arch: f[2]}
	}), nil
}

func (c *Collector) installedPacman(ctx context.Context) ([]apitypes.InstalledPackage, error) {
	// `pacman -Q` lists "name version" — no arch. Fine: pacman is single-arch
	// per system in practice, so we leave Arch empty.
	out, err := c.run(ctx, 30*time.Second, "pacman", "-Q")
	if err != nil {
		return nil, err
	}
	var pkgs []apitypes.InstalledPackage
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		pkgs = append(pkgs, apitypes.InstalledPackage{
			Manager: string(ManagerPacman), Name: f[0], Version: f[1],
		})
	}
	return pkgs, sc.Err()
}

func (c *Collector) installedAPK(ctx context.Context) ([]apitypes.InstalledPackage, error) {
	out, err := c.run(ctx, 30*time.Second, "apk", "info", "-v")
	if err != nil {
		return nil, err
	}
	// `apk info -v` prints lines like "name-1.2.3-r0" — no arch.
	var pkgs []apitypes.InstalledPackage
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		nv := strings.TrimSpace(sc.Text())
		if nv == "" {
			continue
		}
		// Split at the LAST occurrence of "-<digit>" (version separator).
		name, version := splitAPKNameVersion(nv)
		if name == "" {
			continue
		}
		pkgs = append(pkgs, apitypes.InstalledPackage{
			Manager: string(ManagerAPK), Name: name, Version: version,
		})
	}
	return pkgs, sc.Err()
}

// --- Updates lists ---------------------------------------------------------

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

func (c *Collector) updatesPacman(ctx context.Context) ([]apitypes.PendingUpdate, error) {
	// `pacman -Qu` prints "name current_version -> available_version" for
	// every out-of-sync package. Exit code 1 means "no updates", which
	// safeexec reports as an error; we tolerate it.
	out, _ := c.run(ctx, 20*time.Second, "pacman", "-Qu")
	var ups []apitypes.PendingUpdate
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[2] != "->" {
			continue
		}
		ups = append(ups, apitypes.PendingUpdate{
			Manager:          string(ManagerPacman),
			Name:             f[0],
			CurrentVersion:   f[1],
			AvailableVersion: f[3],
		})
	}
	return ups, sc.Err()
}

func (c *Collector) updatesAPT(ctx context.Context) ([]apitypes.PendingUpdate, error) {
	if !safeexec.Available("apt") {
		return nil, nil
	}
	out, err := c.run(ctx, 30*time.Second, "apt", "list", "--upgradable")
	if err != nil {
		return nil, err
	}
	var ups []apitypes.PendingUpdate
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "[upgradable from:") {
			continue
		}
		// Format: "name/repo version arch [upgradable from: oldver]"
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		nameRepo := strings.SplitN(f[0], "/", 2)
		repo := ""
		if len(nameRepo) == 2 {
			repo = nameRepo[1]
		}
		oldVer := strings.TrimSuffix(f[len(f)-1], "]")
		ups = append(ups, apitypes.PendingUpdate{
			Manager:          string(ManagerDpkg),
			Name:             nameRepo[0],
			Arch:             f[2],
			CurrentVersion:   oldVer,
			AvailableVersion: f[1],
			SourceRepo:       repo,
		})
	}
	return ups, sc.Err()
}

func (c *Collector) updatesDNF(ctx context.Context) ([]apitypes.PendingUpdate, error) {
	if !safeexec.Available("dnf") {
		return nil, nil
	}
	// dnf check-update returns 100 when updates exist, 0 when none, others = error.
	// safeexec reports non-zero as error; we ignore that path as a status signal.
	out, _ := c.run(ctx, 60*time.Second, "dnf", "check-update", "--quiet")
	var ups []apitypes.PendingUpdate
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		nameArch := strings.SplitN(f[0], ".", 2)
		arch := ""
		if len(nameArch) == 2 {
			arch = nameArch[1]
		}
		ups = append(ups, apitypes.PendingUpdate{
			Manager:          string(ManagerRPM),
			Name:             nameArch[0],
			Arch:             arch,
			AvailableVersion: f[1],
			SourceRepo:       f[2],
		})
	}
	return ups, sc.Err()
}

func (c *Collector) updatesAPK(ctx context.Context) ([]apitypes.PendingUpdate, error) {
	out, err := c.run(ctx, 20*time.Second, "apk", "version", "-l", "<")
	if err != nil {
		return nil, err
	}
	var ups []apitypes.PendingUpdate
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// expect: name-1.2.3-r0 < 1.2.4-r0
		if len(f) < 3 || f[1] != "<" {
			continue
		}
		name, ver := splitAPKNameVersion(f[0])
		ups = append(ups, apitypes.PendingUpdate{
			Manager:          string(ManagerAPK),
			Name:             name,
			CurrentVersion:   ver,
			AvailableVersion: f[2],
		})
	}
	return ups, sc.Err()
}

// --- Repo metadata freshness ----------------------------------------------

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

// --- helpers ---------------------------------------------------------------

func parseTSV(out []byte, _ Manager, expectedFields int, build func([]string) apitypes.InstalledPackage) []apitypes.InstalledPackage {
	var pkgs []apitypes.InstalledPackage
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < expectedFields {
			continue
		}
		pkgs = append(pkgs, build(f))
	}
	return pkgs
}

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

// splitAPKNameVersion splits "name-1.2.3-r0" into ("name", "1.2.3-r0").
// The last two `-`-separated tokens form the version (release suffix included).
func splitAPKNameVersion(s string) (name, version string) {
	parts := strings.Split(s, "-")
	if len(parts) < 3 {
		return s, ""
	}
	// Find the index where the version token starts. Heuristic: version
	// token starts with a digit.
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 && parts[i][0] >= '0' && parts[i][0] <= '9' {
			name = strings.Join(parts[:i], "-")
			version = strings.Join(parts[i:], "-")
			return
		}
	}
	return s, ""
}

