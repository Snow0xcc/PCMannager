//go:build !windows

package winui

// MenuItem 是 Windows 弹层菜单的条目；非 Windows 平台没有对应概念。
type MenuItem struct {
	Text      string
	Checked   bool
	Separator bool
	// Icon 非 Windows 下忽略（无原生菜单层）。
	Icon func(c *Canvas, box Rect, textColor uint32)
}

// PopupMenu 非 Windows 下恒返回 -1（无选择），调用方按"取消"处理。
func PopupMenu(owner HWND, x, y int32, items []MenuItem) int { return -1 }

// DispatchOwnerDraw 非 Windows 下恒未处理（无 owner-draw 消息）。
func DispatchOwnerDraw(msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	return 0, false
}
