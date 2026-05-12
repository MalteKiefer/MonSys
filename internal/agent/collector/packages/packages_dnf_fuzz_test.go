//go:build linux

package packages

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzDNFLine exercises parseDNFLine with arbitrary single-line input.
// Contract: must never panic; on ok=true Manager equals the RPM constant
// and AvailableVersion is non-empty (parser requires len(fields) >= 3).
// UTF-8 validity is logged but not asserted — see FuzzAPTLine for the same
// rationale.
func FuzzDNFLine(f *testing.F) {
	// Seed with real `dnf check-update` rows plus blanks/junk.
	f.Add("kernel.x86_64 5.14.0-503.21.1.el9_5 baseos")
	f.Add("glibc.x86_64 2.34-100.el9_5 appstream")
	f.Add("openssl-libs.x86_64 1:3.0.7-28.el9_5 baseos")
	f.Add("noarchpkg 1.0 baseos")
	f.Add("")
	f.Add("Obsoleting Packages")

	f.Fuzz(func(t *testing.T, line string) {
		line = strings.ReplaceAll(line, "\n", " ")
		line = strings.ReplaceAll(line, "\r", " ")

		// secNames map is allowed to be nil; parseDNFLine performs map lookups
		// which are safe on a nil map. Verify that contract too.
		var nilMap map[string]bool
		u1, ok1 := parseDNFLine(line, nilMap)
		u2, ok2 := parseDNFLine(line, map[string]bool{})
		if ok1 != ok2 {
			t.Fatalf("parseDNFLine ok differs between nil and empty map for %q", line)
		}
		_ = u2
		if !ok1 {
			return
		}
		if u1.Manager != string(ManagerRPM) {
			t.Fatalf("Manager = %q, want %q (input %q)", u1.Manager, ManagerRPM, line)
		}
		if u1.AvailableVersion == "" {
			t.Fatalf("ok=true but AvailableVersion empty (input %q)", line)
		}
		if u1.SourceRepo == "" {
			t.Fatalf("ok=true but SourceRepo empty (input %q)", line)
		}
		for name, s := range map[string]string{
			"Name": u1.Name, "Arch": u1.Arch,
			"AvailableVersion": u1.AvailableVersion,
			"SourceRepo":       u1.SourceRepo,
		} {
			if !utf8.ValidString(s) {
				t.Logf("note: field %s is not UTF-8: %q (input %q)", name, s, line)
			}
		}
	})
}
