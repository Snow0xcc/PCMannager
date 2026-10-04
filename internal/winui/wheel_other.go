//go:build !windows

package winui

// 滚轮消息是 Win32 专有概念（WM_MOUSEWHEEL/wParam 高字增量、lParam 屏幕坐标），
// 非 Windows 平台各有自己的滚轮事件模型（X11 Button4/5、macOS scrollWheel），
// 不做跨平台模拟。
const WM_MOUSEWHEEL = 0x020A

// WHEEL_DELTA 非 Windows 下仅保留数值，供跨平台代码引用常量时不需条件编译。
const WHEEL_DELTA = 120

// GET_WHEEL_DELTA_WPARAM 非 Windows 下恒返回 0：没有 wParam 就没有增量。
func GET_WHEEL_DELTA_WPARAM(wParam uintptr) int16 { return 0 }
