//go:build windows

package tray

import (
	"errors"
	"syscall"
)

// Shell32/user32/gdi32 procedures used by the tray implementation.
var (
	user32  = syscall.NewLazyDLL("user32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")
	gdi32   = syscall.NewLazyDLL("gdi32.dll")

	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procFillRect            = user32.NewProc("FillRect")
	procDrawTextW           = user32.NewProc("DrawTextW")
	procGetSysColor         = user32.NewProc("GetSysColor")
	procGetSysColorBrush    = user32.NewProc("GetSysColorBrush")

	// gdi32 的归属经过确认：GetTextExtentPoint32W/SetTextColor/SetBkMode 都在
	// gdi32，挂到 user32 会在首次调用时 panic（见 AGENTS.md 的 Win32 句柄归属约定）。
	procGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")
	procSetTextColor          = gdi32.NewProc("SetTextColor")
	procSetBkMode             = gdi32.NewProc("SetBkMode")
	procSelectObject          = gdi32.NewProc("SelectObject")
	procGetStockObject        = gdi32.NewProc("GetStockObject")
	procDeleteObject          = gdi32.NewProc("DeleteObject")
)

// errShellNotify is returned when Shell_NotifyIconW reports failure.
var errShellNotify = errors.New("Shell_NotifyIcon 调用失败")
