//go:build !windows

package app

import (
	"reflect"
	"testing"

	"github.com/snow0xcc/pcmannager/internal/server"
)

// C2-2 热键冲突可视化：Provider 必须把「两个模块配置了同一全局热键」暴露给
// 面板。这是用户唯一可修复的冲突形态——core.HotkeyManager.Bind 会静默丢弃
// 第二个模块的重复绑定，而 HotkeyManager.Conflicts() 因此结构性恒空（详见
// provider.go 的 Conflicts 注释），面板只能从配置层检测。
//
// 用真实 App + fake 模块端到端断言映射：同 combo 两个模块 → 一条冲突，
// holders 保持注册顺序；拼写不同但规范化相同的组合（ctrl+alt+k /
// alt+ctrl+k）必须算同一条。
func TestProviderConflictsReportsSameCombo(t *testing.T) {
	a := newTestApp(t)
	a.MustRegister(newFakeModule("x"))
	a.MustRegister(newFakeModule("y"))
	a.MustRegister(newFakeModule("z")) // 绑定不同组合，不参与冲突

	set := func(id, hk string) {
		t.Helper()
		if err := a.Config().Module(id).SetHotkey(hk); err != nil {
			t.Fatalf("SetHotkey %s=%s: %v", id, hk, err)
		}
	}
	set("x", "ctrl+alt+k")
	set("y", "alt+ctrl+k") // 拼写不同，规范化后与 x 相同
	set("z", "ctrl+alt+j")

	a.RebindHotkeys() // 走真实绑定路径；冲突判定仍基于配置层

	got := a.PanelProvider().Conflicts()
	want := []server.HotkeyConflict{{Hotkey: "ctrl+alt+k", Holders: []string{"x", "y"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Conflicts() = %+v, 期望 %+v", got, want)
	}
}

// TestProviderConflictsEmptyWhenDistinct：不同组合、未绑定（""）与无法解析
// 的热键都不能构成冲突。非法热键经配置层直接写入时可能被接受也可能被拒绝
// （面板路径会拒绝），两种情况下它都无法与任何组合构成冲突。
func TestProviderConflictsEmptyWhenDistinct(t *testing.T) {
	a := newTestApp(t)
	a.MustRegister(newFakeModule("a"))
	a.MustRegister(newFakeModule("b"))
	a.MustRegister(newFakeModule("c"))
	a.MustRegister(newFakeModule("d"))

	set := func(id, hk string) {
		t.Helper()
		if err := a.Config().Module(id).SetHotkey(hk); err != nil {
			t.Fatalf("SetHotkey %s=%s: %v", id, hk, err)
		}
	}
	set("a", "ctrl+alt+f1")
	set("b", "ctrl+alt+f2")
	set("c", "") // 未绑定
	_ = a.Config().Module("d").SetHotkey("ctrl+alt+nosuchkey")

	if got := a.PanelProvider().Conflicts(); len(got) != 0 {
		t.Fatalf("不同组合/未绑定/非法热键不应报冲突: %+v", got)
	}
}

// TestProviderTrayIconPathContract（TODO #9）：tray_icon_path 是应用级配置，
// 必须 (a) 出现在 AppConfig DTO 中、(b) 列入 RestartRequired（托盘图标只在
// 启动时加载，保存后不重启不生效，面板 toast 依赖这份清单）、(c) 经
// PatchAppConfig 持久化。三者缺一就会出现"保存了但静默无效"的假配置。
func TestProviderTrayIconPathContract(t *testing.T) {
	a := newTestApp(t)
	p := a.PanelProvider()

	if got := p.AppConfig().TrayIconPath; got != "" {
		t.Fatalf("默认 TrayIconPath = %q, 期望空串", got)
	}

	found := false
	for _, k := range p.AppConfig().RestartRequired {
		if k == "tray_icon_path" {
			found = true
		}
	}
	if !found {
		t.Fatalf("RestartRequired = %v, 缺少 tray_icon_path（面板将不提示重启）",
			p.AppConfig().RestartRequired)
	}

	v := `D:\icons\brand.ico`
	if err := p.PatchAppConfig(server.AppConfigPatch{TrayIconPath: &v}); err != nil {
		t.Fatalf("PatchAppConfig 失败: %v", err)
	}
	if got := p.AppConfig().TrayIconPath; got != v {
		t.Fatalf("DTO 回读 TrayIconPath = %q, 期望 %q", got, v)
	}
	if got := a.Config().App().TrayIconPath; got != v {
		t.Fatalf("配置持久化 TrayIconPath = %q, 期望 %q", got, v)
	}
}
