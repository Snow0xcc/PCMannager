package app

import (
	"runtime/debug"
	"strings"
)

// Version is the application version reported by the panel (GET /api/state)
// and by the Wails binding.
//
// It is a var rather than a const so release builds can stamp it at link time:
//
//	go build -ldflags "-X github.com/snow0xcc/pcmannager/internal/app.Version=v1.2.3"
//
// scripts/build.sh does exactly that, deriving the value from git describe.
// Builds without stamping (plain `go run .`, IDE builds, tests) fall back to
// versionFromBuildInfo, which recovers the module version from the embedded
// build info, and finally to devVersion when even that is unavailable.
var Version = versionFromBuildInfo()

// devVersion is the last-resort version for unstamped, non-module builds.
const devVersion = "0.0.0-dev"

// NormalizedVersion reports the running version without the leading "v".
//
// scripts/build.sh stamps Version from PCM_VERSION, which CI derives from
// github.ref_name — the tag name itself ("v1.2.3"). The panel used to render
// "v" + state.version, producing "vv1.2.3". Version is data, not presentation:
// every read site goes through here so the stored value may keep its git tag
// form while consumers (panel, Wails binding, updater SemVer baseline) always
// see the bare triple.
func NormalizedVersion() string { return strings.TrimPrefix(Version, "v") }

// versionFromBuildInfo reads the module version Go embeds automatically.
//
// This is what makes `go install github.com/snow0xcc/pcmannager@vX.Y.Z` report
// the installed version without any ldflags. Installed binaries get a real
// version here; local builds report "(devel)" and fall back to devVersion.
func versionFromBuildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return devVersion
	}
	// "(devel)" is Go's placeholder for a build from a source tree rather than
	// a resolved module version. Reporting it to users would be noise.
	if info.Main.Version == "(devel)" {
		return devVersion
	}
	return info.Main.Version
}
