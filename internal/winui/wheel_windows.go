//go:build windows

package winui

// 滚轮消息支撑：供有滚动/缩放内容的窗口（截图编辑器、置顶 pin 窗口）在
// WndProc 里分支处理 WM_MOUSEWHEEL。

// WM_MOUSEWHEEL (0x020A) 之前在本包缺失，这里补上。WM_LBUTTONDBLCLK (0x0203)
// 已在 api_windows.go 声明，无需重复。WHEEL_DELTA（滚轮一格的增量 120）已在
// input_windows.go 声明（SendInput 注入路径也在用），不在此重复定义。
const WM_MOUSEWHEEL = 0x020A

// GET_WHEEL_DELTA_WPARAM extracts the wheel rotation from a WM_MOUSEWHEEL
// wParam: the high word, read as a signed 16-bit value
// （即 C 宏 (short)HIWORD(wParam)）。
//
// 正值 = 滚轮向上推（远离用户，配"放大/向上滚"），负值 = 向下。
func GET_WHEEL_DELTA_WPARAM(wParam uintptr) int16 {
	return int16((wParam >> 16) & 0xFFFF)
}

// HIWORD 提取 32 位参数的高 16 位（GET_WHEEL_DELTA_WPARAM 的底层宏形式，
// 保留命名形式便于调用点与 Win32 文档对照）。
func HIWORD(v uintptr) uint16 { return uint16(v >> 16) }

// WM_MOUSEWHEEL 的 lParam 坐标语义与换算方法：
//
// 与 WM_MOUSEMOVE 等 WM_*BUTTON* 消息不同，WM_MOUSEWHEEL 的 lParam 给的是
// **屏幕坐标**（LOWORD(lParam)=x、HIWORD(lParam)=y，均可能为负——多显示器
// 负向排布时），而不是客户区坐标。要把光标位置换算到窗口客户区，用
// POINT{X: x, Y: y} 先由 ScreenToClient(hwnd, &pt) 转换，再做命中测试；
// 直接把 lParam 的高低字当客户区坐标用，在窗口不在屏幕原点时全部错位。
// （wParam 低 16 位是按键修饰符 MK_*，高位才是滚轮增量，见
// GET_WHEEL_DELTA_WPARAM。）
