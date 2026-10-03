//go:build !windows

package winui

// BeginBufferedPaint 非 Windows 下不可用：双缓冲绘制是 Win32 GDI 的能力，其它
// 平台的绘图栈各自有内建的双缓冲，不做模拟。返回的空闭包保证调用方可以无条件
// defer，与 Windows 失败路径的行为一致。
func BeginBufferedPaint(hwnd HWND) (*Canvas, func(), error) {
	return nil, noopDone, errUnsupported
}

// noopDone 是降级路径的空操作闭包。
func noopDone() {}
