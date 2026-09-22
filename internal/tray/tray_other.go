//go:build !windows

package tray

import (
	"fmt"
	"log/slog"
)

// noopTray keeps GoBox runnable on platforms without a native tray.
//
// GoBox's tray is a Windows Shell_NotifyIcon implementation; other platforms
// compile and run the framework but surface menu actions through the panel.
type noopTray struct{}

// New returns a tray that reports the feature as unavailable.
func New(_ *slog.Logger, _ Handler) Tray { return &noopTray{} }

func (t *noopTray) SetMenu(Menu)  {}
func (t *noopTray) Show() error   { return fmt.Errorf("系统托盘仅支持 Windows") }
func (t *noopTray) Hide()         {}
func (t *noopTray) Visible() bool { return false }
func (t *noopTray) Destroy()      {}
