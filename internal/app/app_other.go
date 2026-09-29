//go:build !windows && !darwin

package app

import "errors"

// Run blocks until the application context is cancelled.
//
// Windows 有 Win32 消息泵、macOS 有 CFRunLoop（见 app_darwin.go）；其余平台
// 没有原生托盘，因此也没有消息泵，首选项面板就是控制面。
func (a *App) Run() { a.Wait() }

// nativePanelAvailable is always false off Windows; OpenPanel therefore uses
// the HTTP panel in the system browser.
func nativePanelAvailable() bool { return false }

// showNativePanel reports the unsupported route if it is called unexpectedly.
// This keeps the platform pair complete without importing Windows UI packages.
func showNativePanel() error { return errors.New("原生面板仅支持 Windows") }

// postQuit is a no-op off Windows: there is no message pump to stop.
func (a *App) postQuit() {}
