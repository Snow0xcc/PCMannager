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

// New returns a tray that reports the feature as unavailable.
// Supported 在没有原生托盘实现的平台上为 false（面板是那些平台的控制面）。
func Supported() bool                    { return false }
func New(_ *slog.Logger, _ Handler) Tray { return &noopTray{} }

func (t *noopTray) SetMenu(Menu)  {}
func (t *noopTray) Show() error   { return fmt.Errorf("系统托盘仅支持 Windows") }
func (t *noopTray) Hide()         {}
func (t *noopTray) Visible() bool { return false }
func (t *noopTray) Destroy()      {}
