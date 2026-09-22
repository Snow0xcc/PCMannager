//go:build windows

package preferences

import (
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"github.com/snow0xcc/pcmannager/internal/config"
)

// Show opens the unified preferences panel for PCMannager. Every feature can
// be toggled independently and assigned its own global hotkey.
func Show(mgr *Manager) {
	cfg := mgr.app.Config

	var mw *walk.MainWindow
	var statusEnabled, clipEnabled, shotEnabled, ctxEnabled, repairEnabled *walk.CheckBox
	var clipHotkey, shotHotkey, ctxHotkey, repairHotkey *walk.LineEdit
	var clipHotOn, shotHotOn, ctxHotOn, repairHotOn *walk.CheckBox
	var cpuChk, ramChk, netChk, diskChk *walk.CheckBox
	var updateMs *walk.NumberEdit

	pageStatus := TabPage{
		Title:  "任务栏状态",
		Layout: VBox{},
		Children: []Widget{
			CheckBox{AssignTo: &statusEnabled, Text: "启用任务栏状态统计", Checked: cfg.Statusbar.Enabled,
				OnCheckedChanged: func() { cfg.Statusbar.Enabled = statusEnabled.Checked() }},
			CheckBox{AssignTo: &cpuChk, Text: "显示 CPU 占用", Checked: cfg.Statusbar.ShowCPU,
				OnCheckedChanged: func() { cfg.Statusbar.ShowCPU = cpuChk.Checked() }},
			CheckBox{AssignTo: &ramChk, Text: "显示内存占用", Checked: cfg.Statusbar.ShowRAM,
				OnCheckedChanged: func() { cfg.Statusbar.ShowRAM = ramChk.Checked() }},
			CheckBox{AssignTo: &netChk, Text: "显示网络速率", Checked: cfg.Statusbar.ShowNet,
				OnCheckedChanged: func() { cfg.Statusbar.ShowNet = netChk.Checked() }},
			CheckBox{AssignTo: &diskChk, Text: "显示磁盘占用", Checked: cfg.Statusbar.ShowDisk,
				OnCheckedChanged: func() { cfg.Statusbar.ShowDisk = diskChk.Checked() }},
			Composite{Layout: HBox{}, Children: []Widget{
				Label{Text: "刷新间隔(ms):"},
				NumberEdit{AssignTo: &updateMs, Value: float64(cfg.Statusbar.UpdateMs), MinValue: 250, MaxValue: 10000, Suffix: " ms",
					OnValueChanged: func() { cfg.Statusbar.UpdateMs = int(updateMs.Value()) }},
			}},
		},
	}

	pageClip := featurePage("剪贴板管理器", &clipEnabled, &clipHotOn, &clipHotkey, &cfg.Clipboard)
	pageShot := featurePage("截图工具", &shotEnabled, &shotHotOn, &shotHotkey, &cfg.Screenshot)
	pageCtx := featurePage("上下文记录", &ctxEnabled, &ctxHotOn, &ctxHotkey, &cfg.SelfContext)
	pageRepair := featurePage("电脑修复与工具", &repairEnabled, &repairHotOn, &repairHotkey, &cfg.PCRepair)

	MainWindow{
		AssignTo: &mw,
		Title:    "PCMannager - 首选项",
		MinSize:  Size{Width: 480, Height: 420},
		Layout:   VBox{},
		Children: []Widget{
			TabWidget{
				Pages: []TabPage{pageStatus, pageClip, pageShot, pageCtx, pageRepair},
			},
			Composite{Layout: HBox{}, Children: []Widget{
				PushButton{Text: "保存", OnClicked: func() {
					if err := mgr.app.Save(); err != nil {
						mgr.app.Log.Errorf("save config: %v", err)
					}
					mgr.ReloadHotkeys()
					mw.Close()
				}},
				PushButton{Text: "取消", OnClicked: func() { mw.Close() }},
			}},
		},
	}.Run()
}

// featurePage builds a generic enable + hotkey tab for a feature.
func featurePage(title string, enabled, hotOn **walk.CheckBox, hotkey **walk.LineEdit, fc *config.FeatureConfig) TabPage {
	return TabPage{
		Title:  title,
		Layout: VBox{},
		Children: []Widget{
			CheckBox{AssignTo: enabled, Text: "启用该功能", Checked: fc.Enabled,
				OnCheckedChanged: func() { fc.Enabled = (*enabled).Checked() }},
			Composite{Layout: HBox{}, Children: []Widget{
				CheckBox{AssignTo: hotOn, Text: "启用快捷键", Checked: fc.HotkeyOn,
					OnCheckedChanged: func() { fc.HotkeyOn = (*hotOn).Checked() }},
				Label{Text: "快捷键:"},
				LineEdit{AssignTo: hotkey, Text: fc.Hotkey,
					OnTextChanged: func() { fc.Hotkey = (*hotkey).Text() }},
			}},
			Label{Text: "格式示例: ctrl+alt+v / f1 / ctrl+shift+c"},
		},
	}
}
