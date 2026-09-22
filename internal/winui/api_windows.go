//go:build windows

package winui

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

// Lazily bound DLL procedures. LazyDLL keeps the import list dependency-free
// and, unlike cgo, needs no C toolchain at build time.
var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	procFindWindowW                = user32.NewProc("FindWindowW")
	procFindWindowExW              = user32.NewProc("FindWindowExW")
	procCreateWindowExW            = user32.NewProc("CreateWindowExW")
	procDestroyWindow              = user32.NewProc("DestroyWindow")
	procDefWindowProcW             = user32.NewProc("DefWindowProcW")
	procRegisterClassExW           = user32.NewProc("RegisterClassExW")
	procUnregisterClassW           = user32.NewProc("UnregisterClassW")
	procSetParent                  = user32.NewProc("SetParent")
	procSetWindowPos               = user32.NewProc("SetWindowPos")
	procGetWindowRect              = user32.NewProc("GetWindowRect")
	procGetClientRect              = user32.NewProc("GetClientRect")
	procMoveWindow                 = user32.NewProc("MoveWindow")
	procShowWindow                 = user32.NewProc("ShowWindow")
	procIsWindow                   = user32.NewProc("IsWindow")
	procIsWindowVisible            = user32.NewProc("IsWindowVisible")
	procGetMessageW                = user32.NewProc("GetMessageW")
	procTranslateMessage           = user32.NewProc("TranslateMessage")
	procDispatchMessageW           = user32.NewProc("DispatchMessageW")
	procPostQuitMessage            = user32.NewProc("PostQuitMessage")
	procPostMessageW               = user32.NewProc("PostMessageW")
	procSendMessageW               = user32.NewProc("SendMessageW")
	procSetTimer                   = user32.NewProc("SetTimer")
	procKillTimer                  = user32.NewProc("KillTimer")
	procInvalidateRect             = user32.NewProc("InvalidateRect")
	procBeginPaint                 = user32.NewProc("BeginPaint")
	procEndPaint                   = user32.NewProc("EndPaint")
	procGetDC                      = user32.NewProc("GetDC")
	procReleaseDC                  = user32.NewProc("ReleaseDC")
	procRegisterWindowMessage      = user32.NewProc("RegisterWindowMessageW")
	procGetSystemMetrics           = user32.NewProc("GetSystemMetrics")
	procSystemParametersInfo       = user32.NewProc("SystemParametersInfoW")
	procGetCursorPos               = user32.NewProc("GetCursorPos")
	procTrackPopupMenu             = user32.NewProc("TrackPopupMenu")
	procCreatePopupMenu            = user32.NewProc("CreatePopupMenu")
	procAppendMenuW                = user32.NewProc("AppendMenuW")
	procDestroyMenu                = user32.NewProc("DestroyMenu")
	procSetForegroundWindow        = user32.NewProc("SetForegroundWindow")
	procGetForegroundWindow        = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW             = user32.NewProc("GetWindowTextW")
	procGetClassNameW              = user32.NewProc("GetClassNameW")
	procGetWindowThreadProcessId   = user32.NewProc("GetWindowThreadProcessId")
	procEnumWindows                = user32.NewProc("EnumWindows")
	procLoadCursorW                = user32.NewProc("LoadCursorW")
	procLoadImageW                 = user32.NewProc("LoadImageW")
	procSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	procSetWindowLongPtrW          = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtrW          = user32.NewProc("GetWindowLongPtrW")
	procUpdateWindow               = user32.NewProc("UpdateWindow")
	procFillRect                   = user32.NewProc("FillRect")
	procDrawTextW                  = user32.NewProc("DrawTextW")
	procSetBkMode                  = user32.NewProc("SetBkMode")
	procSetTextColor               = user32.NewProc("SetTextColor")
	procMessageBoxW                = user32.NewProc("MessageBoxW")

	procCreateSolidBrush      = gdi32.NewProc("CreateSolidBrush")
	procCreateFontIndirectW   = gdi32.NewProc("CreateFontIndirectW")
	procCreateFontW           = gdi32.NewProc("CreateFontW")
	procDeleteObject          = gdi32.NewProc("DeleteObject")
	procSelectObject          = gdi32.NewProc("SelectObject")
	procGetStockObject        = gdi32.NewProc("GetStockObject")
	procTextOutW              = gdi32.NewProc("TextOutW")
	procGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")
	procSetDCBrushColor       = gdi32.NewProc("SetDCBrushColor")
	procCreateCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC              = gdi32.NewProc("DeleteDC")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")

	procSetProcessDpiAwareness        = shcore.NewProc("SetProcessDpiAwareness")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")

	procDwmGetColorizationColor = dwmapi.NewProc("DwmGetColorizationColor")

	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentProcessId = kernel32.NewProc("GetCurrentProcessId")
	procGetModuleFileNameW  = kernel32.NewProc("GetModuleFileNameW")
)

// Win32 constants used across the package.
const (
	SWP_NOZORDER     = 0x0004
	SWP_NOACTIVATE   = 0x0010
	SWP_SHOWWINDOW   = 0x0040
	SWP_HIDEWINDOW   = 0x0080
	SWP_FRAMECHANGED = 0x0020

	SW_HIDE   = 0
	SW_SHOW   = 5
	SW_SHOWNA = 8

	WS_CHILD         = 0x40000000
	WS_POPUP         = 0x80000000
	WS_VISIBLE       = 0x10000000
	WS_CLIPSIBLINGS  = 0x04000000
	WS_EX_TOOLWINDOW = 0x00000080
	WS_EX_TOPMOST    = 0x00000008
	WS_EX_NOACTIVATE = 0x08000000
	WS_EX_LAYERED    = 0x00080000

	CS_HREDRAW = 0x0002
	CS_VREDRAW = 0x0001
	CS_DBLCLKS = 0x0008

	WM_PAINT         = 0x000F
	WM_DESTROY       = 0x0002
	WM_TIMER         = 0x0113
	WM_CLOSE         = 0x0010
	WM_ERASEBKGND    = 0x0014
	WM_SETTINGCHANGE = 0x001A
	WM_CONTEXTMENU   = 0x007B
	WM_RBUTTONUP     = 0x0205
	WM_LBUTTONDBLCLK = 0x0203
	WM_MOUSEMOVE     = 0x0200
	WM_DISPLAYCHANGE = 0x007E
	WM_DPICHANGED    = 0x02E0
	WM_NCDESTROY     = 0x0082
	WM_APP           = 0x8000
	WM_TRAYCALLBACK  = WM_APP + 1

	MF_STRING       = 0x00000000
	MF_SEPARATOR    = 0x00000800
	MF_CHECKED      = 0x00000008
	MF_GRAYED       = 0x00000001
	TPM_RIGHTBUTTON = 0x0002
	TPM_RETURNCMD   = 0x0100

	TRANSPARENT = 1
	IDC_ARROW   = 32512

	SM_CXSCREEN = 0
	SM_CYSCREEN = 1

	NIM_ADD    = 0
	NIM_MODIFY = 1
	NIM_DELETE = 2

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004

	DT_LEFT         = 0x00000000
	DT_RIGHT        = 0x00000002
	DT_CENTER       = 0x00000001
	DT_VCENTER      = 0x00000004
	DT_SINGLELINE   = 0x00000020
	DT_NOPREFIX     = 0x00000800
	DT_END_ELLIPSIS = 0x00008000

	FW_NORMAL         = 400
	DEFAULT_CHARSET   = 1
	DEFAULT_PITCH     = 0
	FF_DONTCARE       = 0
	CLEARTYPE_QUALITY = 5

	MB_OK            = 0x00000000
	MB_ICONWARNING   = 0x00000030
	MB_SETFOREGROUND = 0x00010000

	DPI_AWARENESS_PER_MONITOR = 2
)

// POINT mirrors the Win32 POINT structure.
type POINT struct{ X, Y int32 }

// MSG mirrors the Win32 MSG structure.
type MSG struct {
	HWnd    HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
}

// WNDCLASSEX mirrors the parts of Win32 WNDCLASSEX that GoBox sets.
type WNDCLASSEX struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// LOGFONTW mirrors the Win32 LOGFONTW structure for font creation.
type LOGFONTW struct {
	Height         int32
	Width          int32
	Escapement     int32
	Orientation    int32
	Weight         int32
	Italic         byte
	Underline      byte
	StrikeOut      byte
	CharSet        byte
	OutPrecision   byte
	ClipPrecision  byte
	Quality        byte
	PitchAndFamily byte
	FaceName       [32]uint16
}

// PAINTSTRUCT mirrors the Win32 PAINTSTRUCT structure.
type PAINTSTRUCT struct {
	HDC       uintptr
	Erase     int32
	RcPaint   Rect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

// NOTIFYICONDATAW mirrors the Shell_NotifyIcon payload (Vista+ layout).
type NOTIFYICONDATAW struct {
	Size             uint32
	HWnd             HWND
	ID               uint32
	Flags            uint32
	Callback         uint32
	Icon             uintptr
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GuidItem         [16]byte
	BalloonIcon      uintptr
}

// utf16Ptr converts a Go string into a NUL-terminated UTF-16 buffer.
func utf16Ptr(s string) (*uint16, error) {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ErrNotSupported is returned by operations unavailable on this platform.
var ErrNotSupported = fmt.Errorf("winui: 当前平台不支持该操作")

var classSeq struct {
	sync.Mutex
	n uint32
}

// nextClassID returns a process-unique window class id suffix.
func nextClassID() uint32 {
	classSeq.Lock()
	defer classSeq.Unlock()
	classSeq.n++
	return classSeq.n
}

// getModuleHandle returns the base address of the current executable.
func getModuleHandle() uintptr {
	h, _, _ := procGetModuleHandleW.Call(0)
	return h
}

// lastErr converts a syscall result into a Go error when the call failed.
func lastErr(r1 uintptr) error {
	if r1 != 0 {
		return nil
	}
	if err := syscall.GetLastError(); err != nil && err != syscall.Errno(0) {
		return err
	}
	return fmt.Errorf("win32 调用失败")
}

// unsafePtr exposes unsafe.Pointer conversion for callers in this package.
func unsafePtr[T any](v *T) uintptr { return uintptr(unsafe.Pointer(v)) }
