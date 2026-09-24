// Package updater — SemVer 2.0.0 parsing and comparison for release tags.
//
// Only the subset needed to answer "is this tag newer than the running
// version" is implemented: v-prefixed tags (v1.2.3, v1.2.3-rc.1) and bare
// triples. Anything unparsable yields nil and callers must treat that as
// "not newer" so a malformed tag never triggers an update.
package updater

import (
	"strconv"
	"strings"
)

// semver is a parsed semantic version.
type semver struct {
	Major, Minor, Patch int
	// Prerelease is the text after '-' ("" for a final release).
	Prerelease string
}

// parseSemver parses "v1.2.3-rc.1" style tags; nil when unparsable.
func parseSemver(s string) *semver {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return nil
	}
	// Split off any build metadata ("+...") and pre-release ("-[...").
	if i := strings.IndexAny(s, "+"); i >= 0 {
		s = s[:i]
	}
	pre := ""
	if i := strings.Index(s, "-"); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return nil
	}
	var nums [3]int
	for i, p := range parts {
		if p == "" {
			return nil
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		nums[i] = n
	}
	return &semver{Major: nums[0], Minor: nums[1], Patch: nums[2], Prerelease: pre}
}

// LessThan reports whether v < o by numeric triple only (pre-release handled
// separately by comparePrerelease).
func (v *semver) LessThan(o *semver) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

// comparePrerelease orders two pre-release strings per SemVer: a version
// WITHOUT a pre-release is GREATER than one with it; otherwise identifiers
// are compared left to right, numeric before alphanumeric, longer wins ties.
// Returns <0, 0 or >0.
func comparePrerelease(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1 // final release outranks any pre-release
	case b == "":
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		xn, xe := strconv.Atoi(x)
		yn, ye := strconv.Atoi(y)
		switch {
		case xe == nil && ye == nil: // both numeric
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
		case xe == nil: // numeric < alphanumeric
			return -1
		case ye == nil:
			return 1
		default:
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}
