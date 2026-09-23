package app

import "runtime/debug"

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
