//go:build linux

package packages

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzAPTLine exercises parseAPTLine with arbitrary single-line input.
// Contract: must never panic and on ok=true the returned PendingUpdate has the
// correct Manager and the AvailableVersion field is populated (parseAPTLine
// pulls it from f[1] which is always present when len(f)>=6).
//
// We do NOT assert UTF-8 validity of fields: apt output is locale-dependent
// and the agent passes bytes through to JSON, which will replace invalid
// runes with U+FFFD on encode. The fuzz corpus captures one such input
// (testdata/fuzz/FuzzAPTLine/...) as a regression seed in case we later
// decide to enforce UTF-8 at parse time.
func FuzzAPTLine(f *testing.F) {
	// Seed with real-world apt list --upgradable lines plus a few edge cases.
	f.Add("libc6/noble-updates,noble-security 2.39-0ubuntu8.5 amd64 [upgradable from: 2.39-0ubuntu8.4]")
	f.Add("openssh-server/noble-security 1:9.6p1-3ubuntu13.13 amd64 [upgradable from: 1:9.6p1-3ubuntu13.11]")
	f.Add("curl/jammy-updates 7.81.0-1ubuntu1.20 amd64 [upgradable from: 7.81.0-1ubuntu1.19]")
	f.Add("Listing... Done")
	f.Add("")
	f.Add("badly-formatted line with no slash 1.0 amd64 [upgradable from: 0.9]")

	f.Fuzz(func(t *testing.T, line string) {
		// Drop newlines; parseAPTLine operates on single lines.
		line = strings.ReplaceAll(line, "\n", " ")
		line = strings.ReplaceAll(line, "\r", " ")

		u, ok := parseAPTLine(line)
		if !ok {
			return
		}
		if u.Manager != string(ManagerDpkg) {
			t.Fatalf("Manager = %q, want %q (input %q)", u.Manager, ManagerDpkg, line)
		}
		// parseAPTLine accepts when len(strings.Fields(line)) >= 6, so
		// AvailableVersion (f[1]) and Arch (f[2]) must always be non-empty.
		if u.AvailableVersion == "" {
			t.Fatalf("ok=true but AvailableVersion empty (input %q)", line)
		}
		if u.Arch == "" {
			t.Fatalf("ok=true but Arch empty (input %q)", line)
		}
		// Sanity: don't burn invariants on UTF-8 — log only.
		for name, s := range map[string]string{
			"Name": u.Name, "Arch": u.Arch,
			"CurrentVersion": u.CurrentVersion, "AvailableVersion": u.AvailableVersion,
			"SourceRepo": u.SourceRepo,
		} {
			if !utf8.ValidString(s) {
				t.Logf("note: field %s is not UTF-8: %q (input %q)", name, s, line)
			}
		}
	})
}

// FuzzAPTRepoIsSecurity exercises aptRepoIsSecurity with arbitrary repo
// strings. Contract: must never panic and must return a bool — there is no
// false negative we can assert without re-implementing the function, but we
// can lock in that anything with "-security" as the literal suffix of a
// comma/whitespace-trimmed segment returns true.
func FuzzAPTRepoIsSecurity(f *testing.F) {
	f.Add("noble-updates,noble-security")
	f.Add("bookworm-security")
	f.Add("noble-security/main")
	f.Add("noble-updates")
	f.Add("")
	f.Add(",,,")

	f.Fuzz(func(t *testing.T, repo string) {
		got := aptRepoIsSecurity(repo)
		// Cross-check: if any segment trimmed+lowercased has exact "-security"
		// suffix, the function must return true.
		for _, seg := range strings.Split(repo, ",") {
			seg = strings.ToLower(strings.TrimSpace(seg))
			if strings.HasSuffix(seg, "-security") && !got {
				t.Fatalf("aptRepoIsSecurity(%q) = false, but segment %q ends in -security", repo, seg)
			}
		}
	})
}
