// Package tray — symbolic menu text colors shared by every platform.
//
// 标准 Win32 菜单不支持文字颜色（AppendMenuW/SetMenuItemInfo 都没有颜色
// 字段），唯一轻量做法是 owner-draw（见 tray_windows.go）。本文件只负责
// "符号名 → COLORREF" 的映射表，保持平台无关以便单测。
package tray

// colorKeyToCOLORREF maps an Item.Color key to a Win32 COLORREF (0x00BBGGRR,
// i.e. BGR byte order: RGB(r,g,b) = r | g<<8 | b<<16).
//
// 映射表：
//   - "orange" → RGB(255,165,0) = 0x0000A5FF（橙，用于提醒类菜单项）
//   - "black"  → RGB(0,0,0)     = 0x00000000
//
// 未知色值返回 (0, false)，调用方回退到默认文字色（不猜色）。
func colorKeyToCOLORREF(key string) (uint32, bool) {
	switch key {
	case "orange":
		return 0x0000A5FF, true
	case "black":
		return 0x00000000, true
	default:
		return 0, false
	}
}
