//go:build darwin

package main

import "runtime"

// AppKit 要求 UI 调用发生在**主线程**：NSStatusBar / NSMenu 都不是线程安全的，
// 而 Go 的 goroutine 会被调度器换到任意线程上。
//
// init() 运行在主 goroutine 上（也就是进程的主线程），在这里 LockOSThread 能把
// 主 goroutine 钉死在主线程，后续 main() 与 App.Run()（CFRunLoop 泵）就都在
// 主线程上执行，托盘回调自然也落在主线程。
func init() { runtime.LockOSThread() }
