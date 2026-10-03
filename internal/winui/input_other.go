//go:build !windows

package winui

// ScrollWheel 与 SetCursorPos 在非 Windows 下不可用：合成输入需要平台各自的
// 合成/注入 API（XTest、CGEvent 等），语义与权限模型都不同，这里不做模拟。
// 滚动截图的自动滚动据此会得到 false，从而停止而不是空转。
func ScrollWheel(notches int) bool { return false }

// SetCursorPos 非 Windows 下不移动光标，直接返回 false。
func SetCursorPos(x, y int32) bool { return false }
