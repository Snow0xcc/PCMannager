//go:build darwin

package tray

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/snow0xcc/pcmannager/internal/logo"
)

// macOS 实现走 AppKit 的 NSStatusBar（菜单栏右侧的 status item）。
//
// 与 Windows 版一样刻意不用第三方托盘库：主流 Go 托盘库在 macOS 上要 cgo
// （或直接引 C 绑定），会破坏项目 `CGO_ENABLED=0` 的构建保证。这里改用 purego
// 的 Objective-C runtime 绑定——它经 dlopen + libffi 调用，不需要 cgo。
//
// AppKit 不是线程安全的：所有 NSStatusBar / NSMenu 调用都必须发生在**主线程**。
// 由 main_darwin.go 的 init() 把主 goroutine 锁在主线程，app_darwin.go 的 Run()
// 在主线程上泵 CFRunLoop 提供事件驱动。

// nsVariableStatusItemLength 是 AppKit 的 NSVariableStatusItemLength（长度自适应）。
const nsVariableStatusItemLength = -1.0

// nsActivationPolicyAccessory 是 NSApplicationActivationPolicyAccessory：
// 有菜单栏 UI、但不出现 Dock 图标、不抢焦点。
const nsActivationPolicyAccessory = 1

// trayIconPixels 是托盘图标的像素尺寸：菜单栏图标约 18pt，按 2x 渲染以免发虚。
// 固定像素而不是 setSize:，是因为 NSSize 是按值传的结构体，纯 Go 侧不便安全构造。
const trayIconPixels = 36

// nsStateOn / nsStateOff 对应 NSControlStateValueOn / Off。
const (
	nsStateOn  = 1
	nsStateOff = 0
)

// loadAppKit 显式加载 Foundation 与 AppKit。
//
// 纯 Go 二进制不会链接这两个框架，objc_getClass 对 NSStatusBar / NSString 之类
// 会直接返回 0。本机一度"能跑"是因为某个依赖（剪贴板库）顺带载入了 AppKit，
// 属于偶然：换依赖版本或裁掉那个模块，托盘就会静默退化成没有图标。这里显式
// dlopen 把这件事变成确定的。
var (
	appKitOnce sync.Once
	appKitErr  error
)

func loadAppKit() error {
	appKitOnce.Do(func() {
		for _, path := range []string{
			"/System/Library/Frameworks/Foundation.framework/Foundation",
			"/System/Library/Frameworks/AppKit.framework/AppKit",
		} {
			if _, err := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_LAZY); err != nil {
				appKitErr = fmt.Errorf("加载 %s 失败: %w", path, err)
				return
			}
		}
	})
	return appKitErr
}

// darwinTray 持有一个 status item 及其菜单。
type darwinTray struct {
	log     *slog.Logger
	handler Handler

	mu      sync.Mutex
	menu    Menu
	item    objc.ID // NSStatusItem
	menuObj objc.ID // NSMenu
	target  objc.ID // 自己注册的 target 对象，承载菜单项的 action
	visible bool
	badge   string      // 当前角标文本（空 = 无角标）
	baseImg *image.RGBA // 品牌底图缓存：角标频繁变化时避免反复渲染几何
}

// targetRegistry 把 target 实例（回调里拿到的 self）映射回 tray 实例。
// 用实例指针而不是包级单例，这样"多个托盘"也不会互相串台。
var targetRegistry sync.Map // uintptr(objc.ID) -> *darwinTray

var (
	targetClassOnce sync.Once
	targetClass     objc.Class
	targetClassErr  error
)

// ensureTargetClass 注册 NSObject 子类 PCMTrayTarget，它的 pcmItemClicked: 就是菜单项 action。
//
// 为什么必须自己注册类：菜单点击靠 target/action 投递，action 需要一个能被
// Objective-C 调用的 IMP；purego 的 NewIMP 能把 Go 函数包成 C 函数指针（libffi），
// 这是纯 Go 方案里唯一能拿到 IMP 的途径。
func ensureTargetClass() (objc.Class, error) {
	if err := loadAppKit(); err != nil {
		return 0, err
	}
	targetClassOnce.Do(func() {
		cls, err := objc.RegisterClass("PCMTrayTarget", objc.GetClass("NSObject"), nil, nil,
			[]objc.MethodDef{{Cmd: objc.RegisterName("pcmItemClicked:"), Fn: itemClicked}})
		if err != nil {
			targetClassErr = err
			return
		}
		targetClass = cls
	})
	return targetClass, targetClassErr
}

// itemClicked 是菜单项的 action：从 sender 的 representedObject 取回 Item.ID。
//
// 运行在 AppKit 主线程上，因此与 Windows 版保持一致——同步派发 Handler，且
// 不在持锁状态调用（避免 handler 反过来调 SetMenu 造成重入死锁）。
func itemClicked(self objc.ID, _ objc.SEL, sender objc.ID) {
	raw, ok := targetRegistry.Load(uintptr(self))
	if !ok {
		return
	}
	t, ok := raw.(*darwinTray)
	if !ok || t == nil {
		return
	}
	id := representedObjectString(sender)
	if id == "" {
		return
	}
	t.mu.Lock()
	h := t.handler
	t.mu.Unlock()
	if h == nil {
		return
	}
	h.OnSelect(id)
}

// 主线程派发：AppKit 不是线程安全的，而面板/模块会在自己的 goroutine 里改配置，
// 进而调 SetMenu。所有触碰 AppKit 的操作都必须回到主线程执行。
//
// 走 GCD 的 main queue（经 purego 调 libdispatch），而不是 performSelectorOnMainThread:
// ——后者只能带一个 object 参数，包不住任意闭包；Block 可以。
var (
	dispatchOnce         sync.Once
	dispatchGetMainQueue func() uintptr
	dispatchAsync        func(queue uintptr, block uintptr)
	pthreadMainNP        func() int32
)

func initDispatch() {
	dispatchOnce.Do(func() {
		libsys, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_GLOBAL|purego.RTLD_LAZY)
		if err != nil {
			return
		}
		if sym, err := purego.Dlsym(libsys, "dispatch_get_main_queue"); err == nil {
			purego.RegisterFunc(&dispatchGetMainQueue, sym)
		}
		if sym, err := purego.Dlsym(libsys, "dispatch_async"); err == nil {
			purego.RegisterFunc(&dispatchAsync, sym)
		}
		if sym, err := purego.Dlsym(libsys, "pthread_main_np"); err == nil {
			purego.RegisterFunc(&pthreadMainNP, sym)
		}
	})
}

// isMainThread 报告当前线程是不是进程主线程。
func isMainThread() bool {
	initDispatch()
	if pthreadMainNP == nil {
		return false
	}
	return pthreadMainNP() != 0
}

// onMain 把 fn 交给主线程执行：已在主线程就同步跑，否则异步派发（不等待，
// 避免“主线程还没开始泵 run loop”时互相等死）。
func onMain(fn func()) {
	initDispatch()
	if isMainThread() || dispatchAsync == nil || dispatchGetMainQueue == nil {
		fn()
		return
	}
	block := objc.NewBlock(fn)
	dispatchAsync(dispatchGetMainQueue(), uintptr(block))
}

// nsString 把 Go 字符串包成 NSString。
func nsString(s string) objc.ID {
	if err := loadAppKit(); err != nil {
		return 0
	}
	return objc.Send[objc.ID](objc.ID(objc.GetClass("NSString")), objc.RegisterName("stringWithUTF8String:"), s)
}

// representedObjectString 读取 NSMenuItem 的 representedObject（NSString）并转回 Go 字符串。
func representedObjectString(item objc.ID) string {
	obj := objc.Send[objc.ID](item, objc.RegisterName("representedObject"))
	if obj == 0 {
		return ""
	}
	return objc.Send[string](obj, objc.RegisterName("UTF8String"))
}

// nsImageFromRGBA 把品牌图标渲染成 NSImage。
//
// 走 PNG 数据（initWithData:）而不是逐像素构造 NSBitmapImageRep，是因为后者要
// 按值传 NSSize 之类的结构体，纯 Go 侧不便安全构造。
func nsImageFromRGBA(img *image.RGBA) objc.ID {
	if img == nil {
		return 0
	}
	if err := loadAppKit(); err != nil {
		return 0
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return 0
	}
	data := buf.Bytes()
	nsData := objc.Send[objc.ID](objc.ID(objc.GetClass("NSData")), objc.RegisterName("dataWithBytes:length:"),
		unsafe.Pointer(unsafe.SliceData(data)), uint64(len(data)))
	if nsData == 0 {
		return 0
	}
	image := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(objc.GetClass("NSImage")), objc.RegisterName("alloc")),
		objc.RegisterName("initWithData:"), nsData)
	if image == 0 {
		return 0
	}
	// 模板模式会把图标当纯色遮罩，品牌色就没了；显式关掉。
	objc.Send[objc.ID](image, objc.RegisterName("setTemplate:"), false)
	return image
}

// New 创建一个 macOS 菜单栏托盘。
// Supported 在 macOS 上为 true：本文件实现了 NSStatusBar 菜单项。
func Supported() bool { return true }
func New(log *slog.Logger, handler Handler) Tray {
	if log == nil {
		log = slog.Default()
	}
	return &darwinTray{log: log, handler: handler}
}

// Show 创建 status item 并安装菜单。必须在主线程调用（AppKit 约束）。
//
// 本项目里 StartTray 就在主 goroutine 上，而 main_darwin.go 已把主 goroutine
// 锁在进程主线程，所以这里天然满足；非主线程调用会直接报错而不是冒险去动
// AppKit——那样往往不是报错，而是某个时刻的随机崩溃。
func (t *darwinTray) Show() error {
	if err := loadAppKit(); err != nil {
		return err
	}
	if !isMainThread() {
		return fmt.Errorf("NSStatusBar 必须在主线程创建（当前非主线程）")
	}
	if _, err := ensureTargetClass(); err != nil {
		return fmt.Errorf("注册托盘 target 类失败: %w", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.visible {
		return nil
	}

	// 菜单栏程序需要一个 sharedApplication，并把激活策略设为 accessory。
	if app := objc.Send[objc.ID](objc.ID(objc.GetClass("NSApplication")), objc.RegisterName("sharedApplication")); app != 0 {
		objc.Send[objc.ID](app, objc.RegisterName("setActivationPolicy:"), nsActivationPolicyAccessory)
	}

	bar := objc.Send[objc.ID](objc.ID(objc.GetClass("NSStatusBar")), objc.RegisterName("systemStatusBar"))
	if bar == 0 {
		return fmt.Errorf("无法获取 NSStatusBar")
	}
	item := objc.Send[objc.ID](bar, objc.RegisterName("statusItemWithLength:"), nsVariableStatusItemLength)
	if item == 0 {
		return fmt.Errorf("无法创建 NSStatusItem")
	}
	// status item 由系统持有，这里 retain 一份避免被 autorelease 池回收。
	objc.Send[objc.ID](item, objc.RegisterName("retain"))
	t.item = item

	if button := objc.Send[objc.ID](item, objc.RegisterName("button")); button != 0 {
		if img := nsImageFromRGBA(t.composedIcon()); img != 0 {
			objc.Send[objc.ID](button, objc.RegisterName("setImage:"), img)
		}
		objc.Send[objc.ID](button, objc.RegisterName("setToolTip:"), nsString(t.menu.Tooltip))
	}

	if err := t.buildMenuLocked(); err != nil {
		return err
	}
	// 交给系统负责点击弹菜单（statusItem.menu），不必自己 popUp。
	objc.Send[objc.ID](item, objc.RegisterName("setMenu:"), t.menuObj)
	t.visible = true
	return nil
}

// Hide 移除菜单栏图标。
func (t *darwinTray) Hide() {
	// 可能来自任意 goroutine（Shutdown 路径），回到主线程再动 AppKit。
	onMain(t.hideOnMain)
}

// hideOnMain 是 Hide 的主线程实现。
func (t *darwinTray) hideOnMain() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.visible || t.item == 0 {
		return
	}
	if bar := objc.Send[objc.ID](objc.ID(objc.GetClass("NSStatusBar")), objc.RegisterName("systemStatusBar")); bar != 0 {
		objc.Send[objc.ID](bar, objc.RegisterName("removeStatusItem:"), t.item)
	}
	objc.Send[objc.ID](t.item, objc.RegisterName("release"))
	t.item = 0
	t.visible = false
}

// Visible 报告图标是否在菜单栏上。
func (t *darwinTray) Visible() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.visible
}

// SetBadge 更新角标。可从任意 goroutine 调用：AppKit 操作经 onMain 回主线程。
//
// 频繁调用（剪贴板每复制一次就更新）在 36px 下合成 + PNG 编码的开销可以忽略，
// 真正昂贵的是几何渲染，因此品牌底图只渲染一次并缓存。
func (t *darwinTray) SetBadge(text string) {
	t.mu.Lock()
	if t.badge == text {
		t.mu.Unlock()
		return
	}
	t.badge = text
	t.mu.Unlock()
	onMain(t.rebadgeOnMain)
}

// rebadgeOnMain 把带角标的图标重新铺到状态栏按钮上（主线程）。
func (t *darwinTray) rebadgeOnMain() {
	t.mu.Lock()
	visible := t.visible && t.item != 0
	img := t.composedIcon()
	t.mu.Unlock()
	if !visible {
		return
	}
	if button := objc.Send[objc.ID](t.item, objc.RegisterName("button")); button != 0 && img != nil {
		objc.Send[objc.ID](button, objc.RegisterName("setImage:"), nsImageFromRGBA(img))
	}
}

// composedIcon 返回叠加了当前角标的托盘底图（可在任意线程调用：纯 Go 绘制）。
func (t *darwinTray) composedIcon() *image.RGBA {
	t.mu.Lock()
	badge := t.badge
	t.mu.Unlock()
	if t.baseImg == nil {
		t.baseImg = logo.Render(trayIconPixels)
	}
	return BadgeOverlay(t.baseImg, badge)
}

// SetMenu 重建菜单。
func (t *darwinTray) SetMenu(m Menu) {
	t.mu.Lock()
	t.menu = m
	visible := t.visible
	t.mu.Unlock()
	if !visible {
		return
	}
	// 调用方多半是模块/面板的 goroutine，AppKit 必须在主线程上动。
	onMain(t.rebuildOnMain)
}

// rebuildOnMain 是 SetMenu 的主线程实现。
func (t *darwinTray) rebuildOnMain() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.visible {
		return
	}
	if err := t.buildMenuLocked(); err != nil {
		t.log.Warn("重建托盘菜单失败", "err", err)
		return
	}
	objc.Send[objc.ID](t.item, objc.RegisterName("setMenu:"), t.menuObj)
	if button := objc.Send[objc.ID](t.item, objc.RegisterName("button")); button != 0 && t.menu.Tooltip != "" {
		objc.Send[objc.ID](button, objc.RegisterName("setToolTip:"), nsString(t.menu.Tooltip))
	}
}

// Destroy 释放资源。
func (t *darwinTray) Destroy() {
	t.Hide()
	onMain(t.releaseOnMain)
}

// releaseOnMain 释放菜单与 target（主线程）。
func (t *darwinTray) releaseOnMain() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.menuObj != 0 {
		objc.Send[objc.ID](t.menuObj, objc.RegisterName("release"))
		t.menuObj = 0
	}
	if t.target != 0 {
		targetRegistry.Delete(uintptr(t.target))
		objc.Send[objc.ID](t.target, objc.RegisterName("release"))
		t.target = 0
	}
}

// buildMenuLocked 把 Menu 渲染成 NSMenu。调用方必须持有 mu，且必须在主线程。
func (t *darwinTray) buildMenuLocked() error {
	if t.target == 0 {
		cls, err := ensureTargetClass()
		if err != nil {
			return err
		}
		target := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(cls), objc.RegisterName("alloc")), objc.RegisterName("init"))
		if target == 0 {
			return fmt.Errorf("无法创建托盘 target")
		}
		objc.Send[objc.ID](target, objc.RegisterName("retain"))
		targetRegistry.Store(uintptr(target), t)
		t.target = target
	}

	if t.menuObj != 0 {
		objc.Send[objc.ID](t.menuObj, objc.RegisterName("release"))
		t.menuObj = 0
	}
	menu := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(objc.GetClass("NSMenu")), objc.RegisterName("alloc")), objc.RegisterName("init"))
	if menu == 0 {
		return fmt.Errorf("无法创建 NSMenu")
	}
	// 关掉自动启用：启用与否完全由 Item.Disabled 决定，否则系统会自作主张置灰。
	objc.Send[objc.ID](menu, objc.RegisterName("setAutoenablesItems:"), false)

	sel := objc.RegisterName("pcmItemClicked:")
	for _, it := range t.menu.Items {
		if it.Separator {
			if sep := objc.Send[objc.ID](objc.ID(objc.GetClass("NSMenuItem")), objc.RegisterName("separatorItem")); sep != 0 {
				objc.Send[objc.ID](menu, objc.RegisterName("addItem:"), sep)
			}
			continue
		}
		mi := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(objc.GetClass("NSMenuItem")), objc.RegisterName("alloc")),
			objc.RegisterName("initWithTitle:action:keyEquivalent:"), nsString(it.Title), sel, nsString(""))
		if mi == 0 {
			continue
		}
		// representedObject 记住 Item.ID，点击时据此回调。
		objc.Send[objc.ID](mi, objc.RegisterName("setRepresentedObject:"), nsString(it.ID))
		objc.Send[objc.ID](mi, objc.RegisterName("setTarget:"), t.target)
		objc.Send[objc.ID](mi, objc.RegisterName("setAction:"), sel)
		if it.Checkable {
			state := nsStateOff
			if it.Checked {
				state = nsStateOn
			}
			objc.Send[objc.ID](mi, objc.RegisterName("setState:"), state)
		}
		objc.Send[objc.ID](mi, objc.RegisterName("setEnabled:"), !it.Disabled)
		// Item.Color 在 macOS 上不着色：NSMenuItem 的 attributedTitle 能上色，
		// 但会覆盖系统对禁用/勾选态的呈现，收益不抵风险，保持系统外观。
		objc.Send[objc.ID](menu, objc.RegisterName("addItem:"), mi)
	}
	t.menuObj = menu
	return nil
}
