//go:build linux

package security

import (
	"bufio"
	"bytes"
	"context"
	"strconv"
	"strings"

	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// fail2banJails returns the per-jail status for every jail listed by
// `fail2ban-client status`. Missing tool, daemon down or unparsable output
// degrade silently to nil so the agent tick can continue. Each jail is
// queried independently; a failure on one jail does not poison the rest.
func (c *Collector) fail2banJails(ctx context.Context) []apitypes.Fail2banJailInfo {
	if !safeexec.Available("fail2ban-client") {
		return nil
	}
	out, err := safeexec.RunWithTimeout(ctx, fail2banCmdTimeout, "fail2ban-client", "status")
	if err != nil {
		logger.Debug("fail2ban-client status failed", "err", err)
		return nil
	}
	jailLine := ""
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Jail list:") {
			_, jailLine, _ = strings.Cut(line, ":")
			break
		}
	}
	if jailLine == "" {
		return nil
	}
	var jails []apitypes.Fail2banJailInfo
	for _, jail := range strings.Split(jailLine, ",") {
		jail = strings.TrimSpace(jail)
		if jail == "" {
			continue
		}
		info, err := fail2banJailDetail(ctx, jail)
		if err != nil {
			logger.Debug("fail2ban jail detail failed", "jail", jail, "err", err)
			continue
		}
		jails = append(jails, info)
	}
	return jails
}

// fail2banJailDetail runs `fail2ban-client status <jail>` and parses its
// "Currently failed / Total failed / Currently banned / Total banned /
// Banned IP list" key-value output. The keys are matched with HasSuffix
// because fail2ban prefixes them with a hyphen/bullet that differs slightly
// between versions.
func fail2banJailDetail(ctx context.Context, jail string) (apitypes.Fail2banJailInfo, error) {
	out, err := safeexec.RunWithTimeout(ctx, fail2banCmdTimeout, "fail2ban-client", "status", jail)
	if err != nil {
		return apitypes.Fail2banJailInfo{}, err
	}
	info := apitypes.Fail2banJailInfo{Jail: jail}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch {
		case strings.HasSuffix(key, "Currently failed"):
			info.CurrentlyFailed = atoiSafe(val)
		case strings.HasSuffix(key, "Total failed"):
			info.TotalFailed = atoiSafe(val)
		case strings.HasSuffix(key, "Currently banned"):
			info.CurrentlyBanned = atoiSafe(val)
		case strings.HasSuffix(key, "Total banned"):
			info.TotalBanned = atoiSafe(val)
		case strings.HasSuffix(key, "Banned IP list"):
			if val != "" {
				info.BannedIPs = strings.Fields(val)
			}
		}
	}
	return info, nil
}

// atoiSafe parses s as an integer, returning 0 on any error. fail2ban's
// counters are advisory and never negative; treating malformed values as 0
// keeps the report shape stable across daemon versions.
func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
