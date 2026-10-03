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

// FocusedWindow reports no window off Windows: there is no cross-platform way
// to name the focused control without a native toolkit, and callers (the
// clipboard write-back) only use it to aim a synthetic paste, which is itself
// Windows-only. Returning Invalid makes that path report "unsupported" rather
// than misdirecting a paste.
func FocusedWindow() HWND { return Invalid }
