//go:build windows

package winui

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

func platformCapabilities() CapabilitiesInfo {
	return CapabilitiesInfo{
		Native:           true,
		TaskbarEmbedding: true,
		TrayIcon:         true,
		Toasts:           true,
		Elevation:        true,
		Hotkeys:          true,
	}
}

// SetDPIAware enables per-monitor DPI awareness so text stays crisp on scaled
// displays. Best-effort: older Windows builds simply ignore it.
func SetDPIAware() {
	// Try the per-monitor-v2 context first (Windows 10 1703+).
	if err := procSetProcessDpiAwarenessContext.Find(); err == nil {
		const dpiAwarenessPerMonitorV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		if r, _, _ := procSetProcessDpiAwarenessContext.Call(dpiAwarenessPerMonitorV2); r != 0 {
			return
		}
	}
	if err := procSetProcessDpiAwareness.Find(); err == nil {
		_, _, _ = procSetProcessDpiAwareness.Call(uintptr(DPI_AWARENESS_PER_MONITOR))
	}
	// Fall back to the legacy system-DPI switch.
	const spiSetProcessDPIAware = 0x0095
	_, _, _ = procSystemParametersInfo.Call(spiSetProcessDPIAware, 0, 0, 0)
}

// FindWindow looks up a top-level window by class and title ("" = any).
func FindWindow(className, windowName string) HWND {
	var cls, title *uint16
	if className != "" {
		cls, _ = syscall.UTF16PtrFromString(className)
	}
	if windowName != "" {
		title, _ = syscall.UTF16PtrFromString(windowName)
	}
	h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)))
	return HWND(h)
}

// FindWindowEx finds a child window by class/title.
func FindWindowEx(parent, after HWND, className, windowName string) HWND {
	var cls, title *uint16
	if className != "" {
		cls, _ = syscall.UTF16PtrFromString(className)
	}
	if windowName != "" {
		title, _ = syscall.UTF16PtrFromString(windowName)
	}
	h, _, _ := procFindWindowExW.Call(uintptr(parent), uintptr(after),
		uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)))
	return HWND(h)
}

// TaskbarClassName is the primary taskbar window class.
const TaskbarClassName = "Shell_TrayWnd"

// SecondaryTaskbarClassName is the class of secondary-monitor taskbars.
const SecondaryTaskbarClassName = "Shell_SecondaryTrayWnd"

// FindTaskbar locates the primary taskbar window.
func FindTaskbar() HWND { return FindWindow(TaskbarClassName, "") }

// FindSecondaryTaskbars enumerates secondary-monitor taskbars.
func FindSecondaryTaskbars() []HWND {
	var out []HWND
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		name := ClassName(HWND(hwnd))
		if name == SecondaryTaskbarClassName {
			out = append(out, HWND(hwnd))
		}
		return 1 // keep enumerating
	})
	r, _, _ := procEnumWindows.Call(cb, 0)
	runtime.KeepAlive(cb)
	_ = r
	return out
}

// ClassName returns a window's class name.
func ClassName(h HWND) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetClassNameW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

// WindowText returns a window's title text.
func WindowText(h HWND) string {
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

// WindowRect returns a window's bounding rectangle in screen coordinates.
func WindowRect(h HWND) Rect {
	var r Rect
	procGetWindowRect.Call(uintptr(h), uintptr(unsafe.Pointer(&r)))
	return r
}

// ClientRect returns a window's client rectangle.
func ClientRect(h HWND) Rect {
	var r Rect
	procGetClientRect.Call(uintptr(h), uintptr(unsafe.Pointer(&r)))
	return r
}

// IsWindow reports whether the handle refers to an existing window.
func IsWindow(h HWND) bool {
	r, _, _ := procIsWindow.Call(uintptr(h))
	return r != 0
}

// IsWindowVisible reports whether the window is visible.
func IsWindowVisible(h HWND) bool {
	r, _, _ := procIsWindowVisible.Call(uintptr(h))
	return r != 0
}

// SetParent reparents a window (the taskbar-embedding primitive).
func SetParent(child, parent HWND) HWND {
	r, _, _ := procSetParent.Call(uintptr(child), uintptr(parent))
	return HWND(r)
}

// ShowWindow changes a window's visibility.
func ShowWindow(h HWND, cmd int32) bool {
	r, _, _ := procShowWindow.Call(uintptr(h), uintptr(cmd))
	return r != 0
}

// MoveWindow repositions and resizes a window.
func MoveWindow(h HWND, x, y, w, hgt int32, repaint bool) error {
	rp := uintptr(0)
	if repaint {
		rp = 1
	}
	r, _, err := procMoveWindow.Call(uintptr(h), uintptr(x), uintptr(y),
		uintptr(w), uintptr(hgt), rp)
	if r == 0 {
		if err != nil && err != syscall.Errno(0) {
			return err
		}
		return fmt.Errorf("MoveWindow 失败")
	}
	return nil
}

// SetWindowPos repositions a window with explicit flags.
func SetWindowPos(h, after HWND, x, y, w, hgt int32, flags uint32) error {
	r, _, err := procSetWindowPos.Call(uintptr(h), uintptr(after),
		uintptr(x), uintptr(y), uintptr(w), uintptr(hgt), uintptr(flags))
	if r == 0 {
		if err != nil && err != syscall.Errno(0) {
			return err
		}
		return fmt.Errorf("SetWindowPos 失败")
	}
	return nil
}

// DestroyWindow closes a window.
func DestroyWindow(h HWND) bool {
	r, _, _ := procDestroyWindow.Call(uintptr(h))
	return r != 0
}

// PostMessage posts a message to a window's queue without blocking.
func PostMessage(h HWND, msg uint32, wParam, lParam uintptr) {
	_, _, _ = procPostMessageW.Call(uintptr(h), uintptr(msg), wParam, lParam)
}

// SendMessage sends a message and waits for it to be processed.
func SendMessage(h HWND, msg uint32, wParam, lParam uintptr) uintptr {
	r, _, _ := procSendMessageW.Call(uintptr(h), uintptr(msg), wParam, lParam)
	return r
}

// SetTimer installs a timer on a window; ms is the interval.
func SetTimer(h HWND, id uintptr, ms uint32) uintptr {
	r, _, _ := procSetTimer.Call(uintptr(h), id, uintptr(ms), 0)
	return r
}

// KillTimer removes a previously installed timer.
func KillTimer(h HWND, id uintptr) { _, _, _ = procKillTimer.Call(uintptr(h), id) }

// InvalidateRect schedules a repaint.
func InvalidateRect(h HWND) { _, _, _ = procInvalidateRect.Call(uintptr(h), 0, 0) }

// RegisterWindowMessage registers a broadcast message id (e.g. TaskbarCreated).
func RegisterWindowMessage(name string) uint32 {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	r, _, _ := procRegisterWindowMessage.Call(uintptr(unsafe.Pointer(p)))
	return uint32(r)
}

// ScreenSize returns the primary display size in pixels.
func ScreenSize() (int32, int32) {
	w, _, _ := procGetSystemMetrics.Call(SM_CXSCREEN)
	h, _, _ := procGetSystemMetrics.Call(SM_CYSCREEN)
	return int32(w), int32(h)
}

// CursorPos returns the current mouse position.
func CursorPos() POINT {
	var p POINT
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p
}

// ForegroundWindow returns the currently focused window.
func ForegroundWindow() HWND {
	r, _, _ := procGetForegroundWindow.Call()
	return HWND(r)
}

// ProcessID returns the owning process id of a window.
func ProcessID(h HWND) uint32 {
	var pid uint32
	_, _, _ = procGetWindowThreadProcessId.Call(uintptr(h), uintptr(unsafe.Pointer(&pid)))
	return pid
}

// DarkModeEnabled reports whether the Windows "app" theme is dark.
func DarkModeEnabled() bool {
	const (
		hkeyCurrentUser = 0x80000001
		keyRead         = 0x20019
	)
	path, _ := syscall.UTF16PtrFromString(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`)
	var key uintptr
	if r, _, _ := advapiRegOpenKeyEx.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(path)), 0, keyRead, uintptr(unsafe.Pointer(&key))); r != 0 {
		return false // default to light when the key is unreadable
	}
	defer advapiRegCloseKey.Call(key)

	name, _ := syscall.UTF16PtrFromString("SystemUsesLightTheme")
	var (
		val  uint32
		size = uint32(4)
		typ  uint32
	)
	if r, _, _ := advapiRegQueryValueEx.Call(key, uintptr(unsafe.Pointer(name)), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&val)), uintptr(unsafe.Pointer(&size))); r != 0 {
		return false
	}
	return val == 0
}

// TaskbarColor returns the taskbar's accent color as 0x00BBGGRR, falling back
// to a sensible default when the theme API is unavailable.
func TaskbarColor() (uint32, bool) {
	var color uint32
	var opaque uint32
	ok := false
	if err := procDwmGetColorizationColor.Find(); err == nil {
		r, _, _ := procDwmGetColorizationColor.Call(
			uintptr(unsafe.Pointer(&color)), uintptr(unsafe.Pointer(&opaque)))
		ok = r == 0
	}
	if !ok {
		if DarkModeEnabled() {
			return 0x1E1E1E, false // #1E1E1E in BGR
		}
		return 0xF0F0F0, false
	}
	return color, true
}

// MessageLoop runs the standard GetMessage pump until WM_QUIT.
//
// dispatch receives every message before default processing, letting callers
// handle custom messages while returning false to fall through.
func MessageLoop(dispatch func(msg *MSG) bool) {
	var msg MSG
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		if dispatch != nil && dispatch(&msg) {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// PostQuitMessage terminates the current message loop.
func PostQuitMessage(code int32) {
	procPostQuitMessage.Call(uintptr(code))
}

// MessageBox shows a modal message box; returns true when the user accepted.
func MessageBox(title, text string, flags uint32) bool {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	r, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)),
		uintptr(unsafe.Pointer(t)), uintptr(flags))
	return r != 0
}

// Window is a lightweight base window with a Go message handler.
//
// It exists so modules can create native windows without repeating the
// WNDCLASSEX/syscall.NewCallback plumbing, which is easy to get wrong.
type Window struct {
	mu        sync.Mutex
	hwnd      HWND
	className string
	// Handle receives every message; return the result and true to stop
	// default processing.
	Handle func(hwnd HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool)
	// callbacks keeps the syscall callback alive for the window's lifetime.
	callbacks []uintptr
}

// windows maps handles back to their Window so the shared WndProc can dispatch.
var windows = struct {
	sync.RWMutex
	m map[HWND]*Window
}{m: map[HWND]*Window{}}

// NewWindow creates (but does not show) a window with the given style.
func NewWindow(className string, style uint32, exStyle uint32, parent HWND) (*Window, error) {
	w := &Window{className: fmt.Sprintf("%s_GoBox_%d", className, nextClassID())}

	cls, err := syscall.UTF16PtrFromString(w.className)
	if err != nil {
		return nil, err
	}
	inst := getModuleHandle()
	cursor, _, _ := procLoadCursorW.Call(0, IDC_ARROW)

	cb := syscall.NewCallback(wndProcShared)
	w.callbacks = append(w.callbacks, cb)

	wc := WNDCLASSEX{
		Size:      uint32(unsafe.Sizeof(WNDCLASSEX{})),
		Style:     CS_HREDRAW | CS_VREDRAW | CS_DBLCLKS,
		WndProc:   cb,
		Instance:  inst,
		Cursor:    cursor,
		ClassName: cls,
	}
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		if err != nil && err != syscall.Errno(0) {
			return nil, fmt.Errorf("RegisterClassEx 失败: %w", err)
		}
		return nil, fmt.Errorf("RegisterClassEx 失败")
	}

	name, _ := syscall.UTF16PtrFromString(w.className)
	title, _ := syscall.UTF16PtrFromString("")
	hwnd, _, err := procCreateWindowExW.Call(
		uintptr(exStyle), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)),
		uintptr(style), 0, 0, 0, 0,
		uintptr(parent), 0, inst, 0,
	)
	if hwnd == 0 {
		if err != nil && err != syscall.Errno(0) {
			return nil, fmt.Errorf("CreateWindowEx 失败: %w", err)
		}
		return nil, fmt.Errorf("CreateWindowEx 失败")
	}
	w.hwnd = HWND(hwnd)

	windows.Lock()
	windows.m[w.hwnd] = w
	windows.Unlock()
	return w, nil
}

// wndProcShared routes messages to the owning Window.
func wndProcShared(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	h := HWND(hwnd)

	if msg == WM_NCDESTROY {
		windows.Lock()
		delete(windows.m, h)
		windows.Unlock()
	}

	windows.RLock()
	w := windows.m[h]
	windows.RUnlock()

	if w != nil && w.Handle != nil {
		if res, handled := w.Handle(h, msg, wParam, lParam); handled {
			return res
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

// HWND returns the underlying window handle.
func (w *Window) HWND() HWND { return w.hwnd }

// Show makes the window visible.
func (w *Window) Show() { ShowWindow(w.hwnd, SW_SHOW) }

// Hide hides the window.
func (w *Window) Hide() { ShowWindow(w.hwnd, SW_HIDE) }

// Destroy closes the window.
func (w *Window) Destroy() {
	if w.hwnd.Valid() {
		DestroyWindow(w.hwnd)
	}
}

// KeepAlive prevents the callbacks backing this window from being collected.
func (w *Window) KeepAlive() { runtime.KeepAlive(w.callbacks) }
