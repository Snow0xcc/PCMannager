//go:build windows

package tray

import (
	"errors"
	"syscall"
)

// Shell32/user32 procedures used by the tray implementation.
var (
	user32  = syscall.NewLazyDLL("user32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")

	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procLoadIconW           = user32.NewProc("LoadIconW")
)

// errShellNotify is returned when Shell_NotifyIconW reports failure.
var errShellNotify = errors.New("Shell_NotifyIcon 调用失败")
