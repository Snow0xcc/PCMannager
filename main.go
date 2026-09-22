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
	"github.com/snow0xcc/pcmannager/modules/clipboard"
	"github.com/snow0xcc/pcmannager/modules/repair"
	"github.com/snow0xcc/pcmannager/modules/screenshot"
	"github.com/snow0xcc/pcmannager/modules/selfcontext"
	"github.com/snow0xcc/pcmannager/modules/taskbar"
)

func main() {
	// internal/app resolves the data directory, loads config.yaml, opens the
	// log file and starts the hotkey router.
	if dir := configDirEnv(); dir != "" {
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

	a.InitModules()
	a.StartModules()

	log := a.Log()
	log.Info("GoBox 已启动", "config", a.Config().Path(), "data_dir", a.DataDir())

	// Termination signals must release the hotkeys, the log file and the
	// config file even when no UI is around to request a quit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info("收到退出信号")
		a.Shutdown()
	}()

	// Block until Shutdown cancels the application context.
	a.Wait()
	// Shutdown is idempotent, so a signal-driven shutdown is not repeated.
	a.Shutdown()
}

// configDirEnv keeps the historical PCMANNAGER_CONFIG override working:
// internal/app probes config.yaml relative to the working directory, so a
// directory (or a config file, whose parent directory is used) selected here
// wins over the OS data directory while debugging.
func configDirEnv() string {
	dir := os.Getenv("PCMANNAGER_CONFIG")
	if dir == "" {
		return ""
	}
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		dir = filepath.Dir(dir)
	}
	return dir
}
