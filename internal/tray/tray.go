// Package tray renders the system-tray icon and its menu.
//
// The tray is implemented directly on Win32 (Shell_NotifyIcon) rather than
// through a third-party library: the popular Go tray packages need cgo on
// Linux, which would break GoBox's cgo-free build guarantee (PRD GFR-11).
package tray

// Item is a single menu entry exposed to the application.
type Item struct {
	// ID uniquely identifies the item within a menu rebuild.
	ID string
	// Title is the visible label.
	Title string
	// Checkable marks the item as a toggle; Checked carries its state.
	Checkable bool
	Checked   bool
	// Disabled greys the item out.
	Disabled bool
	// Separator renders a divider instead of a clickable entry.
	Separator bool
	// Color tints the item's text. Empty means the default (system) color.
	// Values are symbolic keys mapped to COLORREF by colorKeyToCOLORREF
	// (e.g. "orange", "black"); an unknown key falls back to the default
	// text color. Standard Win32 menus cannot color text, so colored items
	// are rendered via owner-draw on Windows.
	Color string
}

// Menu is an ordered list of tray menu items.
type Menu struct {
	// Tooltip is the hover text.
	Tooltip string
	Items   []Item
}

// Handler receives menu selections.
type Handler interface {
	// OnSelect is called with the ID of the clicked item.
	OnSelect(id string)
}

// HandlerFunc adapts a function to the Handler interface.
type HandlerFunc func(id string)

// OnSelect implements Handler.
func (f HandlerFunc) OnSelect(id string) { f(id) }

// Supported reports whether this build ships a real tray implementation.
//
// 它和“当前能否真的显示出来”是两件事：Linux 上 D-Bus 会话不可用、或 macOS 上
// 没有菜单栏会话时 Show() 仍会失败（有日志），但构建本身带托盘实现。面板用它
// 解释平台差异；之所以不由 winui 统一给出，是因为 winui 不能 import tray
//（tray 依赖 winui，反向会成环）。各平台分别提供实现（tray_{windows,darwin,linux,other}.go）。

// Tray is the platform tray abstraction. Windows 用 Shell_NotifyIcon，
// macOS 用 NSStatusBar，Linux 用 StatusNotifierItem，其余平台是 no-op。
type Tray interface {
	// SetMenu replaces the tray menu.
	SetMenu(Menu)
	// SetBadge 在托盘图标右上角叠加角标（如剪贴板历史条数），空串清除。
	// 角标内容只支持 0-9 与 '+'、'!'，其它字符会被跳过；跨平台外观由
	// BadgeOverlay 统一绘制，方法只需把合成结果换成本平台的图标表示。
	// 可从任意 goroutine 调用（实现方负责线程安全）。
	SetBadge(text string)
	// Show makes the icon visible.
	Show() error
	// Hide removes the icon.
	Hide()
	// Visible reports whether the icon is shown.
	Visible() bool
	// Destroy releases the tray resources.
	Destroy()
}
