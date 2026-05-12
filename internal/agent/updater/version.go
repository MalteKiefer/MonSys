package updater

import (
	"strconv"
	"strings"
)

// # Semver comparison strategy
//
// This package implements a small bespoke semver comparator rather than
// pulling in golang.org/x/mod/semver. The decision is intentional:
//
//   - mon-agent versioning mixes real semvers ("v0.2.0") with rolling
//     describe-style pseudo-versions ("v0.1.5-23-gabcd") emitted by
//     `git describe`. golang.org/x/mod/semver treats the describe suffix
//     as a pre-release tag (so v0.1.5-23-gabcd would sort BEFORE v0.1.5),
//     which is the opposite of what we want for downgrade protection.
//
//   - The numeric components (major.minor.patch) are parsed with
//     strconv.Atoi and compared with cmpInt, so "1.10.0" correctly sorts
//     ABOVE "1.9.0" — a naive lexical comparator would get this wrong.
//     This is the property the AUDIT-402 downgrade check relies on.
//
//   - The "v"/"V" prefix is tolerated, "-dirty" is recognized as a
//     newer-than-clean marker for the same numeric base, and bare commit
//     hashes fall back to a case-insensitive lexical compare so unequal
//     inputs stay distinguishable.
//
// If the project ever drops describe-style version strings and ships only
// real semvers, this file can be replaced with a thin wrapper around
// golang.org/x/mod/semver.Compare. Until then, the custom parser is the
// correct trade-off.

// compareSemver returns -1 if a < b, 0 if equal, +1 if a > b.
//
// It accepts three input shapes:
//
//  1. Real semver: "v1.2.3" or "1.2.3" (with an optional "-pre.N" suffix).
//  2. Rolling pseudo-versions produced by `git describe --tags --always`,
//     of the form "v0.1.5-NN-gSHA[-dirty]" or "0.1.5-NN-gSHA[-dirty]".
//     NN is the number of commits since the latest annotated tag — higher
//     means newer.
//  3. Bare commit hashes ("abc1234") — treated as pseudo with NN=0.
//
// Comparison rules:
//   - Two real semvers compare numerically component-wise (major.minor.patch),
//     so "1.10.0" > "1.9.0".
//   - Two pseudo-versions compare first on the underlying tag, then on NN.
//   - A real semver and a pseudo derived from the same tag: the pseudo wins
//     when it has a positive NN (more commits than the tag), the real semver
//     wins when NN==0 (the pseudo IS the tag).
//   - When we can't extract anything sortable from one side, fall back to a
//     case-insensitive string compare so unequal strings stay distinguishable.
//
// A blank version sorts before any non-blank version.
func compareSemver(a, b string) int {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == b {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}

	pa := parseVersion(a)
	pb := parseVersion(b)

	// If neither side parsed into a usable triplet, fall back to string compare.
	if !pa.ok && !pb.ok {
		return cmpStrings(strings.ToLower(a), strings.ToLower(b))
	}
	if !pa.ok {
		return -1
	}
	if !pb.ok {
		return 1
	}

	// Compare base triplet first. Each component is an int, so 10 > 9.
	if c := cmpInt(pa.major, pb.major); c != 0 {
		return c
	}
	if c := cmpInt(pa.minor, pb.minor); c != 0 {
		return c
	}
	if c := cmpInt(pa.patch, pb.patch); c != 0 {
		return c
	}

	// Same base — pseudo with more commits wins.
	if c := cmpInt(pa.commits, pb.commits); c != 0 {
		return c
	}

	// Identical numeric parts. A "-dirty" build is considered newer than a
	// clean one only when there's nothing else to break the tie, which keeps
	// dev rebuilds from being mistaken for downgrades.
	if pa.dirty != pb.dirty {
		if pa.dirty {
			return 1
		}
		return -1
	}

	// Last-resort: lexical compare of the pre-release/extra suffix. This keeps
	// "1.2.3-rc.2" newer than "1.2.3-rc.1" without parsing prerelease syntax.
	return cmpStrings(strings.ToLower(pa.extra), strings.ToLower(pb.extra))
}

// parsedVersion is the structured form of a version string after
// parseVersion has had a go at it.
type parsedVersion struct {
	major, minor, patch int
	commits             int    // commits-since-tag from `git describe`; 0 means "the tag itself"
	dirty               bool   // describe's "-dirty" suffix
	extra               string // anything left over (e.g. "-rc.1") for lexical tie-break
	ok                  bool   // false when we couldn't extract a major.minor.patch
}

// parseVersion handles "vX.Y.Z", "X.Y.Z", "vX.Y.Z-NN-gSHA", and dirty variants.
func parseVersion(v string) parsedVersion {
	out := parsedVersion{}
	v = strings.TrimSpace(v)
	if v == "" {
		return out
	}
	// Drop a single leading 'v'/'V'.
	if v[0] == 'v' || v[0] == 'V' {
		v = v[1:]
	}
	// Detect & strip the "-dirty" suffix anywhere it lands; describe always
	// places it at the very end.
	if strings.HasSuffix(v, "-dirty") {
		out.dirty = true
		v = strings.TrimSuffix(v, "-dirty")
	}

	// Split off the first three dot-separated segments as major.minor.patch.
	rest := v
	parts := strings.SplitN(rest, ".", 3)
	if len(parts) < 3 {
		// Not enough dots to be a real semver; could still be a bare commit hash.
		return out
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return out
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return out
	}

	// parts[2] is "Z[-NN-gSHA][...extra]"; pull leading digits as patch.
	tail := parts[2]
	patchEnd := 0
	for patchEnd < len(tail) && tail[patchEnd] >= '0' && tail[patchEnd] <= '9' {
		patchEnd++
	}
	if patchEnd == 0 {
		return out
	}
	patch, err := strconv.Atoi(tail[:patchEnd])
	if err != nil {
		return out
	}
	out.major, out.minor, out.patch = major, minor, patch
	out.ok = true
	suffix := tail[patchEnd:]

	// Look for "-NN-gSHA" (commit-count + abbrev sha). git describe always
	// places the commit count immediately after the tag.
	if strings.HasPrefix(suffix, "-") {
		seg := strings.TrimPrefix(suffix, "-")
		// Pull the leading digit run.
		i := 0
		for i < len(seg) && seg[i] >= '0' && seg[i] <= '9' {
			i++
		}
		if i > 0 && i < len(seg) && seg[i] == '-' {
			// Confirm what follows looks like "g<hex>" before trusting it as
			// a describe suffix; otherwise it's just a pre-release tag.
			tail2 := seg[i+1:]
			if len(tail2) > 1 && (tail2[0] == 'g' || tail2[0] == 'G') {
				if n, err := strconv.Atoi(seg[:i]); err == nil {
					out.commits = n
					// Anything past the SHA segment becomes "extra" for lexical tie-break.
					if dash := strings.Index(tail2, "-"); dash >= 0 {
						out.extra = tail2[dash:]
					}
					return out
				}
			}
		}
		// Not a describe suffix — keep it as a pre-release tag for tie-breaking.
		out.extra = suffix
	}
	return out
}

// cmpInt returns -1, 0, +1 for a<b, a==b, a>b.
func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// cmpStrings returns -1, 0, +1 for the byte-lexical order of a and b.
func cmpStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
