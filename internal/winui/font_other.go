//go:build !windows

package winui

// FontFamilies 在非 Windows 下返回 nil。
//
// 字体枚举没有跨平台 API：Linux 走 fontconfig、macOS 走 CoreText，语义与返回
// 结构各不相同；而非 Windows 上本包不渲染任务栏/托盘组件，也没有需要选字体的
// 面板，故不提供降级实现，直接报告 "不可用"。
func FontFamilies() []string { return nil }
