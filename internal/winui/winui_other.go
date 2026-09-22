//go:build !windows

package winui

import "errors"

// errUnsupported is returned by every winui operation on non-Windows builds.
//
// GoBox targets Windows as a first-class platform (PRD §1.4); other platforms
// only need to compile and run the framework, so the entire Win32 layer is
// compiled out rather than emulated.
var errUnsupported = errors.New("winui: 仅 Windows 支持原生窗口能力")

func platformCapabilities() CapabilitiesInfo {
	return CapabilitiesInfo{} // no native UI off Windows
}

// SetDPIAware is a no-op off Windows.
func SetDPIAware() {}

// Elevate is a no-op on non-Windows platforms.
func Elevate(args []string) error { return errUnsupported }

// IsElevated always reports false off Windows.
func IsElevated() bool { return false }
