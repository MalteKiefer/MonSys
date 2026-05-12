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

// installedRPM lists installed packages via `rpm -qa` with an explicit
// --queryformat so the output is tab-separated regardless of locale.
func (c *Collector) installedRPM(ctx context.Context) ([]apitypes.InstalledPackage, error) {
	out, err := c.run(ctx, timeoutInstalled, cmdRPM, "-qa",
		"--queryformat", "%{NAME}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n")
	if err != nil {
		return nil, err
	}
	return parseTSV(out, 3, func(f []string) apitypes.InstalledPackage {
		return apitypes.InstalledPackage{Manager: string(ManagerRPM), Name: f[0], Version: f[1], Arch: f[2]}
	}), nil
}

// updatesDNF lists pending updates via `dnf check-update` and cross-references
// `dnf updateinfo list --security` so we can mark security-relevant rows.
// Returns nil if dnf isn't installed.
//
// `dnf check-update` returns 100 when updates exist, 0 when none, others =
// error. safeexec reports non-zero as error; we treat that as a status signal
// and parse stdout regardless.
func (c *Collector) updatesDNF(ctx context.Context) ([]apitypes.PendingUpdate, error) {
	if !safeexec.Available(cmdDNF) {
		return nil, nil
	}
	out, _ := c.run(ctx, timeoutDNFCheck, cmdDNF, "check-update", "--quiet")

	secNames := c.dnfSecurityNames(ctx)

	var ups []apitypes.PendingUpdate
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if u, ok := parseDNFLine(sc.Text(), secNames); ok {
			ups = append(ups, u)
		}
	}
	return ups, sc.Err()
}

// dnfSecurityNames collects the set of package names dnf flags as having an
// advisory of severity "security". RHEL/Fedora repos don't carry a "-security"
// suffix the way apt does, so a second pass is needed.
//
// Output of `dnf updateinfo list --security --quiet` is "<advisory> <severity>
// <pkg-name-arch>"; we just want the package names. Empty result = no
// security updates pending, which is the desired no-op.
func (c *Collector) dnfSecurityNames(ctx context.Context) map[string]bool {
	secNames := map[string]bool{}
	secOut, _ := c.run(ctx, timeoutDNFCheck, cmdDNF, "updateinfo", "list", "--security", "--quiet")
	sc := bufio.NewScanner(bytes.NewReader(secOut))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		// Strip the trailing ".<arch>" off the package token. dnf prints just
		// "name" in --list mode on most distros; on a few derivatives it emits
		// "name-version-release". We tolerate both — only the leading name is
		// matched against check-update output.
		pkg := f[len(f)-1]
		if dot := strings.LastIndex(pkg, "."); dot > 0 {
			pkg = pkg[:dot]
		}
		secNames[pkg] = true
	}
	return secNames
}

// parseDNFLine extracts one PendingUpdate from a single "dnf check-update" row.
// Format: "<name>.<arch> <available-version> <repo>". Returns ok=false for
// blank lines and the empty separator dnf prints before the obsoleting block.
func parseDNFLine(line string, secNames map[string]bool) (apitypes.PendingUpdate, bool) {
	f := strings.Fields(line)
	if len(f) < 3 {
		return apitypes.PendingUpdate{}, false
	}
	nameArch := strings.SplitN(f[0], ".", 2)
	arch := ""
	if len(nameArch) == 2 {
		arch = nameArch[1]
	}
	return apitypes.PendingUpdate{
		Manager:          string(ManagerRPM),
		Name:             nameArch[0],
		Arch:             arch,
		AvailableVersion: f[1],
		SourceRepo:       f[2],
		IsSecurity:       secNames[nameArch[0]],
	}, true
}
