package main

import (
	"os"
	"path/filepath"

	"github.com/snow0xcc/pcmannager/internal/config"
	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/modules/clipboard"
	"github.com/snow0xcc/pcmannager/modules/repair"
	"github.com/snow0xcc/pcmannager/modules/preferences"
	"github.com/snow0xcc/pcmannager/modules/screenshot"
	"github.com/snow0xcc/pcmannager/modules/selfcontext"
	"github.com/snow0xcc/pcmannager/modules/taskbar"
)

func main() {
	// Resolve config path (env override for dev/testing).
	cfgPath := os.Getenv("PCMANNAGER_CONFIG")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		cfg = config.Default()
	}

	logPath := filepath.Join(filepath.Dir(cfg.Path), "pcmannager.log")
	logger := core.NewLogger(logPath)

	mgr := core.NewManager(cfg, logger)

	// Register every feature. Each is independently toggleable + hotkeyable.
	mgr.Register(statusbar.NewFeature())
	mgr.Register(clipboard.NewFeature())
	mgr.Register(screenshot.NewFeature())
	mgr.Register(selfcontext.NewFeature())
	mgr.Register(pcrepair.NewFeature())

	if err := mgr.Init(); err != nil {
		logger.Errorf("init: %v", err)
	}
	if err := mgr.Start(); err != nil {
		logger.Errorf("start: %v", err)
	}

	// Build the tray menu. The tray message loop (Run) blocks until Quit.
	app := mgr.App()
	app.Tray.Run(func() {
		app.Tray.SetTooltip("PCMannager")
		prefs := app.Tray.AddMenuItem("首选项", "打开设置面板")
		prefs.SetOnClick(func() { preferences.Show(preferences.NewManager(mgr)) })

		app.Tray.AddSeparator()
		openClip := app.Tray.AddMenuItem("剪贴板历史", "打开剪贴板管理器")
		openClip.SetOnClick(func() { _ = mgr.OpenUI("clipboard") })
		openShot := app.Tray.AddMenuItem("截图", "开始截图 (F1)")
		openShot.SetOnClick(func() { _ = mgr.RunHotkey("screenshot") })
		openCtx := app.Tray.AddMenuItem("上下文记录", "查看工作上下文")
		openCtx.SetOnClick(func() { _ = mgr.OpenUI("selfcontext") })
		openRepair := app.Tray.AddMenuItem("电脑修复与工具", "打开修复/安装面板")
		openRepair.SetOnClick(func() { _ = mgr.OpenUI("pcrepair") })

		app.Tray.AddSeparator()
		quit := app.Tray.AddMenuItem("退出", "退出 PCMannager")
		quit.SetOnClick(func() { app.Tray.Quit() })
	}, func() {
		mgr.Stop()
		os.Exit(0)
	})
}
