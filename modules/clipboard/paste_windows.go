//go:build windows

package clipboard

import (
	"fmt"
	"syscall"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// pasteKeyDelayMS gives the focus switch a moment to settle before the paste
// keystrokes are synthesised.
const pasteKeyDelayMS = 120

// Win32 virtual-key codes and event flags used for the synthetic Ctrl+V.
const (
	vkControl   = 0x11
	vkV         = 0x56
	keyEventFUp = 0x0002
)

// user32/kernel32 procs used for synthetic input.
//
// Focus lookup goes through winui (which owns the window layer); only the two
// calls winui does not expose are bound here, both via cgo-free LazyDLL.
var (
	pasteUser32    = syscall.NewLazyDLL("user32.dll")
	procKeybdEvent = pasteUser32.NewProc("keybd_event")
	procSleep      = syscall.NewLazyDLL("kernel32.dll").NewProc("Sleep")
)

// sendPaste synthesises Ctrl+V into hwnd.
//
// hwnd is the window the user was working in BEFORE the history window opened.
// Passing it explicitly matters: the history viewer itself holds the foreground
// while it is on screen, so reading the current foreground at paste time would
// target the viewer (which is about to close) instead of the user's editor.
//
// This is best-effort: the target must accept synthetic input, which elevated
// or otherwise secured windows may refuse. A failure is returned so the caller
// can log a warning without losing the write-back itself.
func sendPaste(hwnd winui.HWND) error {
	if !hwnd.Valid() || !winui.IsWindow(hwnd) {
		return fmt.Errorf("sendPaste: 目标窗口不可用")
	}
	// Restore focus first. Windows may refuse this when another process owns
	// the foreground; that is reported rather than retried (the clipboard write
	// has already succeeded, so the user can still paste manually).
	winui.SetForegroundWindow(hwnd)
	// Activation is asynchronous; give it a moment so the keystrokes land in
	// the window the user was actually working in.
	sleep(pasteKeyDelayMS)

	keybdEvent(vkControl, 0)
	keybdEvent(vkV, 0)
	keybdEvent(vkV, keyEventFUp)
	keybdEvent(vkControl, keyEventFUp)
	sleep(pasteKeyDelayMS)
	return nil
}

// keybdEvent posts one keyboard event through keybd_event.
func keybdEvent(vk byte, flags uint32) {
	_, _, _ = procKeybdEvent.Call(uintptr(vk), 0, uintptr(flags), 0)
}

// sleep waits ms milliseconds (Win32 Sleep).
func sleep(ms uint32) { _, _, _ = procSleep.Call(uintptr(ms)) }
