//go:build !windows && !darwin && !linux

package tray

import (
	"fmt"
	"log/slog"
)

// noopTray keeps GoBox runnable on platforms without a native tray.
//
// macOS 有 NSStatusBar 实现、Linux 有 StatusNotifierItem 实现；其余平台
// 仍然编译运行框架，菜单动作走首选项面板。
type noopTray struct{}

// Supported 在没有原生托盘实现的平台上为 false（面板是那些平台的控制面）。
func Supported() bool { return false }

// New returns a tray that reports the feature as unavailable.
// iconPath is accepted for signature parity with the Windows build but unused.
func New(_ *slog.Logger, _ Handler, _ string) Tray { return &noopTray{} }

// SetMenu 是 no-op。
func (t *noopTray) SetMenu(Menu) {}

// SetBadge 是 no-op：这些平台没有托盘图标可叠加角标。
func (t *noopTray) SetBadge(string) {}

// Show 报告平台不支持。
func (t *noopTray) Show() error { return fmt.Errorf("系统托盘仅支持 Windows") }

// Hide 是 no-op。
func (t *noopTray) Hide() {}

// Visible 恒为 false。
func (t *noopTray) Visible() bool { return false }

// Destroy 是 no-op。
func (t *noopTray) Destroy() {}
