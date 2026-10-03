// Command pcmannager boots the GoBox desktop assistant.
//
// Assembly lives in internal/app: it owns the data directory, the
// configuration, the logger, the event bus and the hotkey router. This file
// only registers the feature modules and blocks until shutdown.
package main

import (
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/snow0xcc/pcmannager/internal/app"
	"github.com/snow0xcc/pcmannager/internal/sysutil"
	"github.com/snow0xcc/pcmannager/modules/clipboard"
	"github.com/snow0xcc/pcmannager/modules/repair"
	"github.com/snow0xcc/pcmannager/modules/screenshot"
	"github.com/snow0xcc/pcmannager/modules/selfcontext"
	"github.com/snow0xcc/pcmannager/modules/taskbar"
	"github.com/snow0xcc/pcmannager/modules/updater"
)

func main() {
	// internal/app resolves the data directory, loads config.yaml, opens the
	// log file and starts the hotkey router.
	if dir, warn := configDirEnv(); warn != "" {
		// The logger is not up yet, so stderr is the only sink. A misconfigured
		// override must be visible rather than silently degrading to default.
		os.Stderr.WriteString(warn + "\n")
	} else if dir != "" {
		if err := os.Chdir(dir); err != nil {
			os.Stderr.WriteString("PCMANNAGER_CONFIG 目录不可用: " + err.Error() + "\n")
		}
	}

	// 单实例：托盘应用不允许双开——两个热键后端/两个托盘图标/剪贴板
	// 监视分裂都会出错。第二次启动静默退出（托盘应用惯例）；GUI 子系统下
	// stderr 不可见，提示可忽略，日志文件里无痕迹属预期。锁在进程内持有，
	// 放在 main 而非 app.New：同进程多 App 实例（测试）不受影响。
	releaseInstance, ok := sysutil.AcquireSingleInstance("pcmannager")
	if !ok {
		os.Stderr.WriteString("PCMannager 已在运行（见系统托盘），本次启动退出。\n")
		os.Exit(1)
	}
	defer releaseInstance()

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

// configDirEnv keeps the historical PCMANNAGER_CONFIG override working:
// internal/app probes config.yaml relative to the working directory, so a
// directory (or a config file, whose parent directory is used) selected here
// wins over the OS data directory while debugging.
// configDirEnv keeps the historical PCMANNAGER_CONFIG override working:
// internal/app probes config.yaml relative to the working directory, so a
// directory (or a config file, whose parent directory is used) selected here
// wins over the OS data directory while debugging.
//
// A non-existent value is NOT silently ignored: it is reported so the operator
// knows the override did not take effect (otherwise the app would silently fall
// back to the OS default and look misconfigured). The caller logs the warning
// and continues with the default directory.
func configDirEnv() (dir string, warn string) {
	raw := os.Getenv("PCMANNAGER_CONFIG")
	if raw == "" {
		return "", ""
	}
	if fi, err := os.Stat(raw); err == nil && !fi.IsDir() {
		return filepath.Dir(raw), ""
	}
	if _, err := os.Stat(raw); err != nil {
		return "", "PCMANNAGER_CONFIG 指向的目录不存在，已忽略并回退到默认数据目录: " + raw
	}
	return raw, ""
}
