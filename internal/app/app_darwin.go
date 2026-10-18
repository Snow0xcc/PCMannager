//go:build darwin

package app

import (
	"errors"
	"time"

	"github.com/ebitengine/purego"
)

// cfStringEncodingUTF8 是 CoreFoundation 的 kCFStringEncodingUTF8。
const cfStringEncodingUTF8 = 0x08000100

// CFRunLoop 绑定：菜单栏图标要靠主线程的 run loop 派发点击，没有它 NSStatusItem
// 的菜单点了不会有反应——这正是 macOS 版托盘不能只靠 a.Wait() 的原因。
var (
	cfRunLoopGetMain   func() uintptr
	cfRunLoopRunInMode func(mode uintptr, seconds float64, returnAfterSourceHandled bool) int32
	cfRunLoopStop      func(loop uintptr)
	// CFStringCreateWithCString 用来造 run loop 的模式字符串，见 init 里的说明。
	cfStringCreateWithCString func(alloc uintptr, cstr string, encoding uint32) uintptr
	cfDefaultMode             uintptr
)

func init() {
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL|purego.RTLD_LAZY)
	if err != nil {
		return
	}
	// 逐个解析：任何一个拿不到就整体降级（runLoopAvailable() 为 false，退回纯等待）。
	if sym, err := purego.Dlsym(cf, "CFRunLoopGetMain"); err == nil {
		purego.RegisterFunc(&cfRunLoopGetMain, sym)
	}
	if sym, err := purego.Dlsym(cf, "CFRunLoopRunInMode"); err == nil {
		purego.RegisterFunc(&cfRunLoopRunInMode, sym)
	}
	if sym, err := purego.Dlsym(cf, "CFRunLoopStop"); err == nil {
		purego.RegisterFunc(&cfRunLoopStop, sym)
	}
	// 运行循环的模式要一个 CFStringRef（值 "kCFRunLoopDefaultMode"）。
	//
	// 不去读 kCFRunLoopDefaultMode 这个**常量**符号：Dlsym 给的是符号地址，而
	// 需要的是它里面存的指针值；把它当函数调（RegisterFunc 后调用）会直接跳到
	// 数据区，实测是 SIGBUS。改用 CFStringCreateWithCString 自己造一个同值的
	// CFString——它内部会拷贝，传入的 Go 字符串无需长期持有，也没有 unsafe 转换，
	// go vet 干净。
	if sym, err := purego.Dlsym(cf, "CFStringCreateWithCString"); err == nil {
		purego.RegisterFunc(&cfStringCreateWithCString, sym)
	}
	if cfStringCreateWithCString != nil {
		cfDefaultMode = cfStringCreateWithCString(0, "kCFRunLoopDefaultMode", cfStringEncodingUTF8)
	}
}

// runLoopAvailable 报告 CFRunLoop 绑定是否可用（缺了就退回纯等待，程序仍可运行）。
func runLoopAvailable() bool {
	return cfRunLoopGetMain != nil && cfRunLoopRunInMode != nil && cfDefaultMode != 0
}

// Run 在主线程上泵 CFRunLoop 直到应用上下文结束。
//
// 非 Windows 的默认实现只是 a.Wait()（等 ctx），但 macOS 的菜单栏图标需要 run
// loop 派发事件，否则图标能显示、菜单点了没反应。这里用有界时间片循环，每次
// 空转最多 100ms，好让 Shutdown 能在一个时间片内被观察到；菜单打开时 AppKit 会
// 自己跑嵌套的 tracking loop，不占用这些时间片。
func (a *App) Run() {
	if !runLoopAvailable() {
		a.Wait()
		return
	}
	const slice = 100 * time.Millisecond
	for {
		select {
		case <-a.ctx.Done():
			return
		default:
		}
		cfRunLoopRunInMode(cfDefaultMode, slice.Seconds(), true)
	}
}

// nativePanelAvailable 在 macOS 上为 false：Wails 原生窗口只在 Windows 装配，
// macOS 走 HTTP 面板（在浏览器里打开）。
func nativePanelAvailable() bool { return false }

// showNativePanel 报告不支持的路由（保持两端成对，供 OpenPanel 的分支调用）。
func showNativePanel() error { return errors.New("原生面板仅支持 Windows") }

// postQuit 提前结束当前 CFRunLoop 时间片，让 Run 的循环尽快观察到 ctx 取消。
//
// 真正的退出由 Shutdown 的 cancel 保证，这里只是把最多 100ms 的等待缩短。
func (a *App) postQuit() { stopRunLoop() }

// stopRunLoop 让 Run 里的循环尽快退出（Shutdown 时调用）。
//
// 只停 run loop 是不够的：循环退出还依赖 ctx 被取消，所以这里只是把当前时间片
// 提前结束，真正的退出条件仍由 Shutdown 的 cancel 保证。
func stopRunLoop() {
	if !runLoopAvailable() {
		return
	}
	cfRunLoopStop(cfRunLoopGetMain())
}
