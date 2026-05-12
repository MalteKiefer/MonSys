//go:build linux

package packages

import (
	"bufio"
	"bytes"
	"context"
	"strings"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// installedPacman lists installed packages via `pacman -Q`. Output is
// "name version" — no arch, since pacman is single-arch per system in practice
// so we leave Arch empty.
func (c *Collector) installedPacman(ctx context.Context) ([]apitypes.InstalledPackage, error) {
	out, err := c.run(ctx, timeoutInstalled, cmdPacman, "-Q")
	if err != nil {
		return nil, err
	}
	var pkgs []apitypes.InstalledPackage
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, scannerInitialBuf), scannerMaxLineBytes)
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

// updatesPacman lists pending updates via `pacman -Qu`. Output is
// "name current_version -> available_version" for every out-of-sync package.
// Exit code 1 means "no updates", which safeexec reports as an error; we
// tolerate that and just parse whatever stdout we got.
func (c *Collector) updatesPacman(ctx context.Context) ([]apitypes.PendingUpdate, error) {
	out, _ := c.run(ctx, timeoutPacmanUpd, cmdPacman, "-Qu")
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
