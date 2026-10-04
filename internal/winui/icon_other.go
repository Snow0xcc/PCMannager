//go:build !windows

package winui

import "image"

// IconFromRGBA 在非 Windows 下返回 0。
//
// HICON 是 Windows GDI 特有的句柄类型，其它平台没有等价物（Linux 用 X11/Wayland
// 各自的像素图，macOS 用 NSImage），且本包在非 Windows 不渲染任何原生窗口与托盘，
// 没有承载图标的窗口，故不提供降级实现，直接报告"创建失败"。
func IconFromRGBA(img *image.RGBA) uintptr { return 0 }

// DestroyIconHandle 在非 Windows 下无事可做：IconFromRGBA 恒返回 0，没有句柄需要
// 释放。
func DestroyIconHandle(h uintptr) {}
