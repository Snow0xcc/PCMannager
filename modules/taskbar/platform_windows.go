//go:build windows

package taskbar

import (
	"fmt"
	"os"
	"strings"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// isWindows reports the host platform.
func isWindows() bool { return true }

// systemDrive returns the Windows system volume root, e.g. "C:\".
func systemDrive() string {
	if sys := os.Getenv("SystemDrive"); sys != "" {
		return strings.TrimRight(sys, `\`) + `\`
	}
	return `C:\`
}

// platformNative reports whether the taskbar widget can be rendered natively.
func platformNative() bool { return true }

// notifyUnsupported is never used on Windows.
func notifyUnsupported(string) {}

// describeWindow returns the taskbar window class used for debugging.
func describeWindow() string {
	return fmt.Sprintf("Shell_TrayWnd hwnd=%d", uintptr(winui.FindTaskbar()))
}
