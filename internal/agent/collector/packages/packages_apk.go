//go:build linux

package packages

import (
	"bufio"
	"bytes"
	"context"
	"strings"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// installedAPK lists installed packages via `apk info -v`. Output is one
// "name-1.2.3-r0" string per line — no arch.
func (c *Collector) installedAPK(ctx context.Context) ([]apitypes.InstalledPackage, error) {
	out, err := c.run(ctx, timeoutInstalled, cmdAPK, "info", "-v")
	if err != nil {
		return nil, err
	}
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

// updatesAPK lists pending updates via `apk version -l '<'`. Each row is
// "name-1.2.3-r0 < 1.2.4-r0" for packages that have a newer version available.
func (c *Collector) updatesAPK(ctx context.Context) ([]apitypes.PendingUpdate, error) {
	out, err := c.run(ctx, timeoutAPKVersion, cmdAPK, "version", "-l", "<")
	if err != nil {
		return nil, err
	}
	var ups []apitypes.PendingUpdate
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
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

// splitAPKNameVersion splits "name-1.2.3-r0" into ("name", "1.2.3-r0").
//
// apk doesn't print a name/version separator, so we use the heuristic that
// the version token begins with a digit. Names with embedded digits (e.g.
// "lib3-foo") are safe because we scan left-to-right and stop at the first
// digit-led segment.
func splitAPKNameVersion(s string) (name, version string) {
	parts := strings.Split(s, "-")
	if len(parts) < 3 {
		return s, ""
	}
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 && parts[i][0] >= '0' && parts[i][0] <= '9' {
			name = strings.Join(parts[:i], "-")
			version = strings.Join(parts[i:], "-")
			return
		}
	}
	return s, ""
}
