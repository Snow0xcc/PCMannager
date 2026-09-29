//go:build darwin

package tray

import (
	"testing"

	"github.com/ebitengine/purego/objc"
	"github.com/snow0xcc/pcmannager/internal/logo"
)

// TestTargetClassRegisters 守护菜单点击所需的 target 类能注册成功。
//
// 菜单项靠 target/action 派发，action 的 IMP 由 purego 从 Go 函数生成；类注册
// 失败的话菜单项点了不会有反应，而且这种失败在运行时是静默的，必须有测试兜住。
func TestTargetClassRegisters(t *testing.T) {
	cls, err := ensureTargetClass()
	if err != nil {
		t.Fatalf("注册 PCMTrayTarget 失败: %v", err)
	}
	if cls == 0 {
		t.Fatal("注册返回的 Class 为 0")
	}
	obj := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(cls), objc.RegisterName("alloc")), objc.RegisterName("init"))
	if obj == 0 {
		t.Fatal("alloc/init 得到 nil 对象")
	}
	// 类必须真的带上了 action 方法，否则点击无从派发。
	if !objc.Send[bool](obj, objc.RegisterName("respondsToSelector:"), objc.RegisterName("pcmItemClicked:")) {
		t.Fatal("target 未响应 pcmItemClicked:")
	}
}

// TestNStringRoundTrip 守护 Item.ID 经 NSString 存取不丢内容：点击时就是靠
// representedObject 把 ID 从菜单项取回来的，转错了会派发到错误的动作。
func TestNStringRoundTrip(t *testing.T) {
	for _, want := range []string{"open_panel", "toggle:taskbar", "退出"} {
		item := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(objc.GetClass("NSMenuItem")), objc.RegisterName("alloc")),
			objc.RegisterName("initWithTitle:action:keyEquivalent:"), nsString(want), objc.RegisterName("pcmItemClicked:"), nsString(""))
		if item == 0 {
			t.Fatalf("创建 NSMenuItem 失败: %q", want)
		}
		objc.Send[objc.ID](item, objc.RegisterName("setRepresentedObject:"), nsString(want))
		if got := representedObjectString(item); got != want {
			t.Fatalf("representedObject = %q, 期望 %q", got, want)
		}
	}
}

// TestRepresentedObjectStringToleratesNil 守护空对象不会把取字符串的路径带崩。
func TestRepresentedObjectStringToleratesNil(t *testing.T) {
	if got := representedObjectString(0); got != "" {
		t.Fatalf("零 ID 应返回空串, 实际 %q", got)
	}
}

// TestSupportedOnDarwin 守护 macOS 构建确实声明了托盘能力（面板据此展示）。
func TestSupportedOnDarwin(t *testing.T) {
	if !Supported() {
		t.Fatal("macOS 应报告托盘可用")
	}
}

// TestNsImageFromRGBAUsesBrandMark 守护托盘图标取自品牌几何而不是空图：
// 渲染失败时返回 0，图标会静默变成空白占位（菜单栏上就是一个空位）。
func TestNsImageFromRGBAUsesBrandMark(t *testing.T) {
	if img := nsImageFromRGBA(logo.Render(trayIconPixels)); img == 0 {
		t.Fatal("品牌图标转成 NSImage 失败")
	}
	// nil 输入必须安全返回 0，而不是崩在一次 dlsym 调用里。
	if img := nsImageFromRGBA(nil); img != 0 {
		t.Fatal("nil 图像应返回 0")
	}
}
