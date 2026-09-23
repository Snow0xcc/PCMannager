//go:build windows

package app

import "github.com/snow0xcc/pcmannager/internal/winui"

// Run blocks on the Win32 message pump, which is what dispatches the tray
// icon's callbacks and menu commands.
//
// It must be called from the goroutine that created the tray window; in
// practice that is main. Shutdown posts WM_QUIT so this returns on exit.
func (a *App) Run() {
	winui.MessageLoop(nil)
}

// postQuit ends the message pump so Run returns.
func (a *App) postQuit() { winui.PostQuitMessage(0) }
