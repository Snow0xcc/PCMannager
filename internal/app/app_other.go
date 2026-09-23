//go:build !windows

package app

import "errors"

// Run blocks until the application context is cancelled.
//
// Non-Windows builds have no native tray and therefore no message pump; the
// preferences panel is the control surface there.
func (a *App) Run() { a.Wait() }

// nativePanelAvailable is always false off Windows; OpenPanel therefore uses
// the HTTP panel in the system browser.
func nativePanelAvailable() bool { return false }

// showNativePanel reports the unsupported route if it is called unexpectedly.
// This keeps the platform pair complete without importing Windows UI packages.
func showNativePanel() error { return errors.New("原生面板仅支持 Windows") }

// postQuit is a no-op off Windows: there is no message pump to stop.
func (a *App) postQuit() {}
