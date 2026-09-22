//go:build !windows

package taskbar

// isWindows reports the host platform.
func isWindows() bool { return false }

// systemDrive is meaningless off Windows; the primary mount is used instead.
func systemDrive() string { return "/" }

// platformNative reports whether the taskbar widget can be rendered natively.
// Off Windows GoBox only collects and logs the metrics.
func platformNative() bool { return false }

// describeWindow has no native window to describe off Windows.
func describeWindow() string { return "no native taskbar (non-Windows)" }
