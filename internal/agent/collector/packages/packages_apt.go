//go:build linux

package packages

import (
	"bufio"
	"bytes"
	"context"
	"strings"

	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// installedDpkg lists installed packages via `dpkg-query`. Output is tab-
// separated by virtue of the explicit -f format string.
func (c *Collector) installedDpkg(ctx context.Context) ([]apitypes.InstalledPackage, error) {
	out, err := c.run(ctx, timeoutInstalled, cmdDpkgQuery, "-W",
		`-f=${binary:Package}\t${Version}\t${Architecture}\n`)
	if err != nil {
		return nil, err
	}
	return parseTSV(out, 3, func(f []string) apitypes.InstalledPackage {
		return apitypes.InstalledPackage{Manager: string(ManagerDpkg), Name: f[0], Version: f[1], Arch: f[2]}
	}), nil
}

// updatesAPT lists pending updates via `apt list --upgradable`. Returns nil
// if apt isn't installed (e.g. Debian minimal containers that only ship dpkg).
func (c *Collector) updatesAPT(ctx context.Context) ([]apitypes.PendingUpdate, error) {
	if !safeexec.Available(cmdAPT) {
		return nil, nil
	}
	out, err := c.run(ctx, timeoutAPTList, cmdAPT, "list", "--upgradable")
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
		if u, ok := parseAPTLine(line); ok {
			ups = append(ups, u)
		}
	}
	return ups, sc.Err()
}

// parseAPTLine extracts one PendingUpdate from a single "apt list --upgradable"
// row. Format: "name/repo version arch [upgradable from: oldver]". repo is
// comma-separated when the package is available from multiple suites, e.g.
// "noble-updates,noble-security".
func parseAPTLine(line string) (apitypes.PendingUpdate, bool) {
	f := strings.Fields(line)
	if len(f) < 6 {
		return apitypes.PendingUpdate{}, false
	}
	nameRepo := strings.SplitN(f[0], "/", 2)
	repo := ""
	if len(nameRepo) == 2 {
		repo = nameRepo[1]
	}
	oldVer := strings.TrimSuffix(f[len(f)-1], "]")
	return apitypes.PendingUpdate{
		Manager:          string(ManagerDpkg),
		Name:             nameRepo[0],
		Arch:             f[2],
		CurrentVersion:   oldVer,
		AvailableVersion: f[1],
		SourceRepo:       repo,
		IsSecurity:       aptRepoIsSecurity(repo),
	}, true
}

// aptRepoIsSecurity returns true if any comma-separated suite segment of the
// apt source carries the "-security" suffix. Covers Debian (bookworm-security,
// bullseye-security), Ubuntu (noble-security, jammy-security), and the
// derivatives that mirror that naming. Case-folded for safety against
// non-standard mirrors.
func aptRepoIsSecurity(repo string) bool {
	if repo == "" {
		return false
	}
	for _, seg := range strings.Split(repo, ",") {
		seg = strings.ToLower(strings.TrimSpace(seg))
		// Match "<suite>-security" anywhere: e.g. "noble-security/main" after
		// splitting on '/' the leading half is "noble-security"; in apt-list
		// output the trailing path is dropped so we just check the suite.
		if strings.HasSuffix(seg, "-security") ||
			strings.Contains(seg, "-security/") {
			return true
		}
	}
	return false
}
