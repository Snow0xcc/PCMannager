//go:build windows

package taskbar

import (
	"fmt"
	"os"
	"strings"
	"time"

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

// powerStatus reads the system battery state via the Win32 power API.
func powerStatus() (percent int, charging bool, present bool) {
	return winui.PowerStatus()
}

// reapStaleWidgets closes leftover widget windows parented under the taskbar.
//
// It is safe when none exist. Requests are posted as WM_CLOSE so each window is
// destroyed on its own thread; the bounded wait makes the close observable before
// the caller creates a replacement.
func reapStaleWidgets() {
	tb := winui.FindTaskbar()
	if !tb.Valid() {
		return
	}
	stale := winui.ChildWindows(tb, widgetClassPrefix)
	if len(stale) == 0 {
		return
	}
	for _, h := range stale {
		winui.PostMessage(h, winui.WM_CLOSE, 0, 0)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(winui.ChildWindows(tb, widgetClassPrefix)) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
