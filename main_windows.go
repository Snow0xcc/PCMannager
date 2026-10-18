package main

import (
	"log/slog"
	"runtime"

	"github.com/snow0xcc/pcmannager/internal/app"
	"github.com/snow0xcc/pcmannager/internal/panel"
	"github.com/snow0xcc/pcmannager/internal/wailsapp"
)

// startNativeWindow opens the preferences panel in a native Wails window.
//
// It runs on its own locked OS thread rather than on main because the Win32
// message pump (a.Run, which drives the tray icon) must stay on the main
// goroutine: Windows windows belong to the thread that created them, and two
// pumps on one thread would starve each other.
//
// The HTTP panel from internal/server is started first and stays up, so if the
// native window is unavailable the browser remains a working fallback.
func startNativeWindow(a *app.App, logf *slog.Logger) {
	if err := wailsapp.Run(wailsapp.Options{
		Title:  "PCMannager 首选项",
		Assets: panel.FS(),
		Logger: logf,
	}, a.PanelProvider()); err != nil {
		// Key/value, not printf: this used to be "…: %v", err handed to
		// slog.Warn, which renders a literal !BADKEY attribute.
		logf.Warn("wailsapp: 原生窗口不可用，继续使用浏览器面板", "err", err)
	}
}

// runNativeWindow launches the native window, if this build provides one.
func runNativeWindow(a *app.App, logf *slog.Logger) {
	go func() {
		// Pinned: Wails/WebView2 requires thread affinity for its window.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		startNativeWindow(a, logf)
	}()
}
