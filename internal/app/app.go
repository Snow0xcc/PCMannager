// Package app orchestrates GoBox: it owns the configuration, the module
// registry, hotkey routing, the tray icon, the event bus and the preferences
// server, and wires them together at boot.
//
// Design note (PRD GFR-12): a module that panics or fails to start must never
// take the whole application down. Every call into module code is therefore
// wrapped with recover + logging.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/snow0xcc/pcmannager/internal/config"
	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/logx"
	"github.com/snow0xcc/pcmannager/internal/paths"
	"github.com/snow0xcc/pcmannager/internal/sysutil"
	"github.com/snow0xcc/pcmannager/internal/winui"
)

// App is the running GoBox application.
type App struct {
	cfgMgr  *config.Manager
	bus     *core.Bus
	log     *slog.Logger
	logFile interface{ Close() error }

	mu      sync.RWMutex
	modules map[string]core.Module
	order   []string
	state   map[string]bool // module id -> running

	hotkeys *core.HotkeyManager

	ctx    context.Context
	cancel context.CancelFunc

	dataDir string

	// panelURL is set once the preferences server is listening.
	panelURL   string
	panelURLMu sync.RWMutex

	// quit is set once shutdown begins so late calls become no-ops.
	quit    bool
	release func()

	wg sync.WaitGroup
}

// New boots the application: directories, config, logger, bus and hotkeys.
//
// It deliberately does not start modules or the tray; Run does that so callers
// (and tests) can inspect the wired-up app first.
func New() (*App, error) {
	// Recover the data directory from an early config read.
	probe, err := config.Load(defaultDataDir())
	if err != nil {
		return nil, err
	}
	dataDir, err := paths.DataDir(probe.Config().App.DataDir)
	if err != nil {
		return nil, err
	}

	// When app.data_dir pointed elsewhere, reload from there so the config
	// file lives next to the data it describes.
	cfgMgr := probe
	if dataDir != defaultDataDir() {
		if cfgMgr, err = config.Load(dataDir); err != nil {
			return nil, err
		}
	}

	bus := core.NewBus()
	logger, closer, err := logx.New(logx.Options{
		Level:   cfgMgr.Config().App.LogLevel,
		File:    paths.LogFile(dataDir),
		Sink:    busSink{bus: bus},
		Console: true,
	})
	if err != nil {
		return nil, fmt.Errorf("初始化日志失败: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	a := &App{
		cfgMgr:  cfgMgr,
		bus:     bus,
		log:     logger,
		logFile: closer,
		modules: map[string]core.Module{},
		state:   map[string]bool{},
		dataDir: dataDir,
		ctx:     ctx,
		cancel:  cancel,
		hotkeys: core.NewHotkeyManager(func(f string, args ...any) {
			// Hotkey problems are warnings: log them and tell the panel.
			msg := fmt.Sprintf(f, args...)
			logger.Warn(msg)
			bus.Log("hotkey", "warn", msg)
		}),
	}
	winui.SetDPIAware()
	return a, nil
}

func defaultDataDir() string { return "" }

// busSink adapts the event bus to the logx.BusSink interface.
type busSink struct{ bus *core.Bus }

func (s busSink) Log(module, level, msg string) { s.bus.Log(module, level, msg) }

// Register adds a module and declares its option defaults.
func (a *App) Register(m core.Module) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, dup := a.modules[m.ID()]; dup {
		return fmt.Errorf("重复的模块 ID: %s", m.ID())
	}
	a.modules[m.ID()] = m
	a.order = append(a.order, m.ID())

	defaults := map[string]any{}
	for _, o := range m.Options() {
		if o.Default != nil {
			defaults[o.Key] = o.Default
		}
	}
	a.cfgMgr.DeclareDefaults(m.ID(), defaults)
	return nil
}

// MustRegister panics on registration failure (boot-time programming error).
func (a *App) MustRegister(m core.Module) {
	if err := a.Register(m); err != nil {
		panic(err)
	}
}

// Log returns the shared logger.
func (a *App) Log() *slog.Logger { return a.log }

// Bus returns the event bus.
func (a *App) Bus() *core.Bus { return a.bus }

// Config returns the configuration manager.
func (a *App) Config() *config.Manager { return a.cfgMgr }

// DataDir returns the application data directory.
func (a *App) DataDir() string { return a.dataDir }

// Context returns the application-wide context.
func (a *App) Context() context.Context { return a.ctx }

// Modules returns the registered modules in declaration order.
func (a *App) Modules() []core.Module {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]core.Module, 0, len(a.order))
	for _, id := range a.order {
		out = append(out, a.modules[id])
	}
	return out
}

// Module returns one module by id.
func (a *App) Module(id string) (core.Module, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	m, ok := a.modules[id]
	return m, ok
}

// Running reports whether a module is currently started.
func (a *App) Running(id string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state[id]
}

// InitModules initialises every module, isolating failures (PRD GFR-12).
func (a *App) InitModules() {
	for _, m := range a.Modules() {
		a.safeCall(m.ID(), "Init", func() error {
			return m.Init(a.moduleContext(m))
		})
	}
}

// StartModules starts every enabled module.
func (a *App) StartModules() {
	a.hotkeys.Start()
	for _, m := range a.Modules() {
		if a.cfgMgr.Module(m.ID()).Enabled() {
			a.Enable(m.ID())
		} else {
			a.log.Info("模块已禁用，跳过启动", "module", m.ID())
		}
	}
}

// Enable starts a single module and (re)binds its hotkey.
func (a *App) Enable(id string) error {
	m, ok := a.Module(id)
	if !ok {
		return fmt.Errorf("未找到模块 %s", id)
	}
	mv := a.cfgMgr.Module(id)
	if mv.Enabled() && a.Running(id) {
		return nil // already running
	}

	if err := a.safeCallErr(id, "Start", m.Start); err != nil {
		a.bus.Log(id, "error", fmt.Sprintf("启动失败: %v", err))
		return err
	}
	a.mu.Lock()
	a.state[id] = true
	a.mu.Unlock()

	a.bindHotkey(m)
	a.log.Info("模块已启动", "module", id)
	a.bus.State(id, map[string]any{"running": true})
	return nil
}

// Disable stops a module and unbinds its hotkey.
func (a *App) Disable(id string) error {
	m, ok := a.Module(id)
	if !ok {
		return fmt.Errorf("未找到模块 %s", id)
	}
	a.hotkeys.Unbind(id)
	a.safeCall(id, "Stop", m.Stop)

	a.mu.Lock()
	a.state[id] = false
	a.mu.Unlock()

	a.log.Info("模块已停止", "module", id)
	a.bus.State(id, map[string]any{"running": false})
	return nil
}

// EnableModule implements core.AppControl for cross-module toggling.
func (a *App) EnableModule(id string, on bool) error {
	if err := a.cfgMgr.Module(id).SetEnabled(on); err != nil {
		return err
	}
	if on {
		return a.Enable(id)
	}
	return a.Disable(id)
}

// bindHotkey registers a module's configured global hotkey.
func (a *App) bindHotkey(m core.Module) {
	id := m.ID()
	hk := a.cfgMgr.Module(id).Hotkey()
	a.hotkeys.Bind(id, hk, func() error {
		return a.safeCallErr(id, "OnHotkey", m.OnHotkey)
	})
}

// RebindHotkeys refreshes every hotkey after a configuration change.
func (a *App) RebindHotkeys() {
	for _, m := range a.Modules() {
		a.bindHotkey(m)
	}
	for _, c := range a.hotkeys.Conflicts() {
		a.bus.Log("hotkey", "warn", "热键冲突: "+c)
	}
}

// RunHotkey manually invokes a module's hotkey action (tray/menu paths).
func (a *App) RunHotkey(id string) error {
	m, ok := a.Module(id)
	if !ok {
		return fmt.Errorf("未找到模块 %s", id)
	}
	return a.safeCallErr(id, "OnHotkey", m.OnHotkey)
}

// OpenUI opens a module's dedicated window.
func (a *App) OpenUI(id string) error {
	m, ok := a.Module(id)
	if !ok {
		return fmt.Errorf("未找到模块 %s", id)
	}
	return a.safeCallErr(id, "OpenUI", m.OpenUI)
}

// ApplyOption applies a runtime option change and, when the module asks for it,
// restarts the module so the new setting takes effect (PRD GFR-4).
func (a *App) ApplyOption(module, key string, value any) error {
	m, ok := a.Module(module)
	if !ok {
		return fmt.Errorf("未找到模块 %s", module)
	}
	mv := a.cfgMgr.Module(module)
	if err := mv.Set(key, value); err != nil {
		return err
	}

	needsRestart := false
	for _, o := range m.Options() {
		if o.Key == key {
			needsRestart = strings.HasPrefix(o.Help, "[restart]")
			break
		}
	}

	if err := a.safeCallErr(module, "ApplyOption", func() error {
		return m.ApplyOption(key, value)
	}); err != nil {
		a.log.Warn("应用配置项失败", "module", module, "key", key, "err", err)
	}

	if needsRestart && a.Running(module) {
		_ = a.Disable(module)
		if err := a.Enable(module); err != nil {
			return err
		}
	}
	a.bus.State(module, map[string]any{"option": key, "value": value})
	return nil
}

// SetModuleSettings applies enabled/hotkey changes from the panel.
func (a *App) SetModuleSettings(id string, enabled *bool, hotkey *string, options map[string]any) error {
	mv := a.cfgMgr.Module(id)
	if hotkey != nil {
		if !core.ValidHotkey(*hotkey) {
			return fmt.Errorf("热键格式无效: %s", *hotkey)
		}
		if err := mv.SetHotkey(*hotkey); err != nil {
			return err
		}
	}
	for k, v := range options {
		if err := a.ApplyOption(id, k, v); err != nil {
			return err
		}
	}
	if hotkey != nil {
		if m, ok := a.Module(id); ok {
			a.bindHotkey(m)
		}
	}
	if enabled != nil {
		if err := a.EnableModule(id, *enabled); err != nil {
			return err
		}
	}
	return nil
}

// Shutdown stops every module and releases resources.
func (a *App) Shutdown() {
	a.mu.Lock()
	if a.quit {
		a.mu.Unlock()
		return
	}
	a.quit = true
	a.mu.Unlock()

	a.log.Info("正在退出 GoBox")
	a.hotkeys.Stop()
	for _, m := range a.Modules() {
		if a.Running(m.ID()) {
			a.safeCall(m.ID(), "Stop", m.Stop)
		}
	}
	a.cancel()
	a.bus.Close()
	if a.logFile != nil {
		_ = a.logFile.Close()
	}
	if a.release != nil {
		a.release()
	}
}

// SetRelease stores the single-instance release callback.
func (a *App) SetRelease(fn func()) { a.release = fn }

// Shutdown implements core.AppControl.
func (a *App) ShutdownAsync() {
	go func() {
		a.Shutdown()
		os.Exit(0)
	}()
}

// SetPanelURL records the preferences server address.
func (a *App) SetPanelURL(u string) {
	a.panelURLMu.Lock()
	a.panelURL = u
	a.panelURLMu.Unlock()
}

// PanelURL returns the preferences panel address.
func (a *App) PanelURL() string {
	a.panelURLMu.RLock()
	defer a.panelURLMu.RUnlock()
	return a.panelURL
}

// OpenPanel opens the preferences panel in the default browser.
func (a *App) OpenPanel() error {
	url := a.PanelURL()
	if url == "" {
		return fmt.Errorf("首选项服务尚未就绪")
	}
	if err := sysutil.OpenURL(url); err == nil {
		return nil
	}
	// Fall back to the platform browser launcher.
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// moduleContext builds the shared core.Context handed to a module.
func (a *App) moduleContext(m core.Module) *core.Context {
	moduleDir, err := paths.ModuleDir(a.dataDir, m.ID())
	if err != nil {
		moduleDir = a.dataDir
		a.log.Warn("无法创建模块数据目录", "module", m.ID(), "err", err)
	}
	return &core.Context{
		Ctx:     a.ctx,
		Logger:  logx.With(a.log, m.ID()),
		Bus:     a.bus,
		Config:  a.cfgMgr.Module(m.ID()),
		App:     a,
		DataDir: moduleDir,
	}
}

// safeCall runs fn, recovering from panics so one module cannot kill GoBox.
func (a *App) safeCall(module, op string, fn func() error) {
	_ = a.safeCallErr(module, op, fn)
}

// safeCallErr is safeCall with the error surfaced to the caller.
func (a *App) safeCallErr(module, op string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("模块 %s 的 %s 发生 panic: %v", module, op, r)
			a.log.Error("模块 panic", "module", module, "op", op, "panic", r)
			a.bus.Log(module, "error", err.Error())
		}
	}()
	return fn()
}

// Wait blocks until the context is cancelled (used by background runners).
func (a *App) Wait() { <-a.ctx.Done() }

// Sleep is a small helper for retry loops that respect cancellation.
func (a *App) Sleep(d time.Duration) bool {
	select {
	case <-a.ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
