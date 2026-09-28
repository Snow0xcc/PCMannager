//go:build !windows

package winui

// Canvas 在 Windows 侧定义于 draw_windows.go（包一层 GDI DC）。非 Windows 下
// 没有 GDI，本文件只保留类型本身，让以 *Canvas 为签名的能力入口（如
// BeginBufferedPaint）在降级分支能通过编译并返回 (nil, noopDone, errUnsupported)；
// 各绘制方法（Fill/Image/Text…）需要真实 GDI，不在此做 no-op 模拟。
type Canvas struct {
	hdc uintptr
}

// DC 返回底层设备上下文句柄；非 Windows 下恒为 0。
func (c *Canvas) DC() uintptr { return c.hdc }
