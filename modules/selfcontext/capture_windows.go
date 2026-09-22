//go:build windows

package selfcontext

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"

	clip "golang.design/x/clipboard"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// ActiveWindow samples the focused window and, in "primary" mode, the process
// that owns it.
//
// The window lookups go through internal/winui so this file stays cgo-free and
// the non-Windows build can compile the rest of the module unchanged.
func ActiveWindow() (title, process string, err error) {
	hwnd := winui.ForegroundWindow()
	if !hwnd.Valid() {
		return "", "", nil
	}
	title = strings.TrimSpace(winui.WindowText(hwnd))
	if title == "" {
		return "", "", nil
	}
	if pid := winui.ProcessID(hwnd); pid != 0 {
		process = processName(pid)
	}
	return title, process, nil
}

// sessionLocked reports whether the interactive session is locked.
//
// Windows shows a dedicated window on the locked desktop and the foreground
// window carries no title there; both are detectable without cgo, which is what
// makes pause_on_lock work on the release build.
func sessionLocked() bool {
	locked := winui.FindWindow("", "Windows Default Lock Screen")
	if locked.Valid() && winui.IsWindowVisible(locked) {
		return true
	}
	fg := winui.ForegroundWindow()
	if !fg.Valid() {
		return true
	}
	return strings.TrimSpace(winui.WindowText(fg)) == ""
}

// copyToClipboard writes text onto the system clipboard so the summary can be
// pasted straight into an LLM prompt.
//
// The clipboard is opened and closed on the calling thread, so this must not
// run on a goroutine that is about to exit; the action handlers call it from the
// panel request goroutine, which outlives the write.
func copyToClipboard(text string) error {
	if text == "" {
		return fmt.Errorf("selfcontext: 摘要为空")
	}
	if err := clip.Init(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := clip.Write(ctx, clip.FmtText, []byte(text)); err != nil {
		return err
	}
	return nil
}

// processName resolves a process id to its executable name (cgo-free LazyDLL).
func processName(pid uint32) string {
	const queryLimitedInformation = 0x1000
	h, err := syscall.OpenProcess(queryLimitedInformation, false, pid)
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)

	const pathBufLen = 260
	var buf [pathBufLen]uint16
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageW.Call(uintptr(h), 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return ""
	}
	name := syscall.UTF16ToString(buf[:size])
	if name == "" {
		return ""
	}
	return name
}

// procQueryFullProcessImageW is the only kernel32 call this file needs.
var procQueryFullProcessImageW = syscall.NewLazyDLL("kernel32.dll").
	NewProc("QueryFullProcessImageNameW")
