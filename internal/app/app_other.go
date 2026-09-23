//go:build !windows

package app

// Run blocks until the application context is cancelled.
//
// Non-Windows builds have no native tray and therefore no message pump; the
// preferences panel is the control surface there.
func (a *App) Run() { a.Wait() }

// postQuit is a no-op off Windows: there is no message pump to stop.
func (a *App) postQuit() {}
