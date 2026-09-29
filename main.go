// Command pcmannager boots the GoBox desktop assistant.
//
// Assembly lives in internal/app: it owns the data directory, the
// configuration, the logger, the event bus and the hotkey router. This file
// only registers the feature modules and blocks until shutdown.
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/snow0xcc/pcmannager/internal/app"
	"github.com/snow0xcc/pcmannager/internal/paths"
	"github.com/snow0xcc/pcmannager/modules/clipboard"
	"github.com/snow0xcc/pcmannager/modules/launcher"
	"github.com/snow0xcc/pcmannager/modules/repair"
	"github.com/snow0xcc/pcmannager/modules/screenshot"
	"github.com/snow0xcc/pcmannager/modules/selfcontext"
	"github.com/snow0xcc/pcmannager/modules/taskbar"
	"github.com/snow0xcc/pcmannager/modules/updater"
)

func main() {
	// internal/app resolves the data directory, loads config.yaml, opens the
	// log file and starts the hotkey router.
	if dir, warn := paths.ConfigDirFromEnv(); warn != "" {
		// The logger is not up yet, so stderr is the only sink. A misconfigured
		// override must be visible rather than silently degrading to default.
		os.Stderr.WriteString(warn + "\n")
	} else if dir != "" {
		if err := os.Chdir(dir); err != nil {
			os.Stderr.WriteString("PCMANNAGER_CONFIG 目录不可用: " + err.Error() + "\n")
		}
	}

	a, err := app.New()
	if err != nil {
		// The logger is not up yet, so stderr is the only sink available.
		os.Stderr.WriteString("启动失败: " + err.Error() + "\n")
		os.Exit(1)
	}

	// Register every feature. Each is independently toggleable + hotkeyable.
	// preferences is not registered: it is a view over this registry, opened
	// on demand through preferences.Show(preferences.NewManager(a)).
	a.MustRegister(taskbar.NewFeature())
	a.MustRegister(clipboard.NewFeature())
	a.MustRegister(screenshot.NewFeature())
	a.MustRegister(selfcontext.NewFeature())
	a.MustRegister(repair.NewFeature())
	a.MustRegister(updater.NewFeature())
	a.MustRegister(launcher.NewFeature())

	// 快捷面板的命令表需要活模块列表：wiring 时把注册表递给它
	// （core.Context 刻意不暴露 registry，见 launcher.SetModulesSource）。
	launcher.SetModulesSource(a.Modules)

	a.InitModules()
	a.StartModules()

	// Reconcile the OS autostart registration with the persisted setting so
	// a config edited by hand (or on another machine) takes effect at boot.
	a.SyncAutostart()

	log := a.Log()

	// The preferences panel is the cross-platform control surface. A bind
	// failure is not fatal: the tray and hotkeys keep working without it.
	if err := a.StartPanel(); err != nil {
		log.Warn("首选项面板未启动", "err", err)
	}

	// The tray is the primary control surface on Windows; elsewhere it is a
	// no-op and the preferences panel takes its place.
	if err := a.StartTray(); err != nil {
		log.Warn("托盘图标不可用，改用首选项面板", "err", err)
	}

	// Native window (Wails/WebView2 on Windows) in addition to the HTTP panel.
	// Both render the same embedded panel and the same provider data, so this
	// is a presentation choice, not a second implementation. off Windows this
	// is a no-op and the browser panel remains the control surface.
	runNativeWindow(a, log.Warn)

	log.Info("PCMannager 已启动", "config", a.Config().Path(), "data_dir", a.DataDir(), "panel", a.PanelURL())

	// Termination signals must release the hotkeys, the log file and the
	// config file even when no UI is around to request a quit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info("收到退出信号")
		a.Shutdown()
	}()

	// Block until Shutdown cancels the application context. On Windows this
	// is the Win32 message pump, which is what drives the tray icon; on other
	// platforms it simply waits for the context.
	a.Run()
	// Shutdown is idempotent, so a signal-driven shutdown is not repeated.
	a.Shutdown()
}
