//go:build windows

package wailsapp

import (
	"runtime/debug"
	"strings"
)

// buildInfo is a seam for tests; it defaults to debug.ReadBuildInfo.
var buildInfo = debug.ReadBuildInfo

// builtWithProductionTag reports whether this binary was compiled with the
// build tag Wails requires.
//
// Why look at the binary's own build info: Wails selects its implementation at
// compile time, and without the tag it silently substitutes a stub whose
// CreateApp only pops a MessageBox. Nothing in the Go type system, and nothing
// at run time before the dialog appears, surfaces that mistake — the user just
// sees an English error box (or, in a GUI-subsystem build with no console, an
// app that does not come up).
//
// The linker records the tag set in the module's build settings, so this is a
// truthful, self-describing check: it answers "how was I built?" rather than
// "is some variable set?".
func builtWithProductionTag() bool {
	bi, ok := buildInfo()
	if !ok {
		// Old toolchains omit build info. Reporting "unknown" as "not stamped"
		// would disable the native window for perfectly good builds, so assume
		// the tag is present and let wails.Run speak for itself.
		return true
	}
	for _, s := range bi.Settings {
		if s.Key == "-tags" {
			return hasProductionTag(s.Value)
		}
	}
	return false
}

// hasProductionTag reports whether a comma-separated tag list contains
// "production" as an exact element. Exact matching matters: Wails' constraint
// is literally `production`, so "productionish" must not satisfy it.
func hasProductionTag(value string) bool {
	for _, tag := range strings.Split(value, ",") {
		if strings.TrimSpace(tag) == "production" {
			return true
		}
	}
	return false
}
