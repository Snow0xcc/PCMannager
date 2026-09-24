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
	"github.com/snow0xcc/pcmannager/internal/server"
	"github.com/snow0xcc/pcmannager/internal/sysutil"
	"github.com/snow0xcc/pcmannager/internal/tray"
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

	// lastError records the most recent failure per module (Start, Stop, a
	// runtime call or a panic) and hotkeyError does the same for hotkey
	// registration; errorAt timestamps the last write to lastError. All three
	// are guarded by mu.
	//
	// They exist because the Windows build runs under -H windowsgui: there is
	// no console, so without them the panel could only report that a module is
	// not running and the user would have to open the log file to learn why.
	// The module and hotkey slots are kept apart so a later hotkey rebind can
	// never erase the reason a module failed to start (and vice versa).
	lastError   map[string]string
	hotkeyError map[string]string
	errorAt     map[string]time.Time

	hotkeys *core.HotkeyManager

	// tray renders the notification-area icon. It is nil when the platform
	// has no native tray (the panel covers those platforms instead).
	tray tray.Tray

	ctx    context.Context
	cancel context.CancelFunc

	dataDir string

	// panelURL is set once the preferences server is listening.
	panelURL   string
	panelURLMu sync.RWMutex

	// panel serves the preferences HTTP API; nil until StartPanel succeeds.
	panel *server.Server

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
		cfgMgr:      cfgMgr,
		bus:         bus,
		log:         logger,
		logFile:     closer,
		modules:     map[string]core.Module{},
		state:       map[string]bool{},
		lastError:   map[string]string{},
		hotkeyError: map[string]string{},
		errorAt:     map[string]time.Time{},
		dataDir:     dataDir,
		ctx:         ctx,
		cancel:      cancel,
	}
	// The warn callback is installed after `a` exists so it can attribute a
	// hotkey problem to the module that owns the combo (see hotkeyWarn).
	a.hotkeys = core.NewHotkeyManager(a.hotkeyWarn)
	// Watch the bus for module-reported failures. Modules publish runtime errors
	// straight to the bus (clipboard losing its X11 display, screenshot failing,
	// …), which on a -H windowsgui build is the only channel they have; the
	// subscriber turns those into panel-visible state. The stop function is
	// registered with the cleanup below so the goroutine cannot outlive the bus.
	a.errorWatch(bus)

	winui.SetDPIAware()
	return a, nil
}

// errorWatch mirrors error-level bus events into each module's error slot.
//
// It reads through Subscribe, so it sees everything published to the bus — not
// just records that came from the logger. Only ids that are registered modules
// are recorded: app-level messages ("app", "hotkey", …) stay in the log rather
// than being attributed to a module.
//
// The goroutine ends when the bus closes (Shutdown), which makes Subscribe's
// channel closed and the range return, so nothing is left running.
func (a *App) errorWatch(bus *core.Bus) {
	ch, stop := bus.Subscribe()
	go func() {
		defer stop()
		for ev := range ch {
			if ev.Type != core.EventLog || ev.Level != "error" || ev.Module == "" {
				continue
			}
			a.recordRuntimeError(ev.Module, ev.Message, ev.Time)
		}
	}()
}

// hotkeyWarn is the warn callback handed to core.HotkeyManager.
//
// Hotkey problems are warnings: they are logged and pushed to the panel, and
// bindHotkey mirrors them into the module's "hotkey" error slot. They never
// stop the module — a lost shortcut is not a stopped module.
func (a *App) hotkeyWarn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	a.log.Warn(msg)
	a.bus.Log("hotkey", "warn", msg)
}

func defaultDataDir() string { return "" }

// busSink adapts the event bus to the logx.BusSink interface.
type busSink struct{ bus *core.Bus }

func (s busSink) Log(module, level, msg string) { s.bus.Log(module, level, msg) }

// recordRuntimeError stores a module-reported runtime failure in that module's
// error slot so the panel can show it. Only known module ids are recorded, so
// app-level messages ("app", "hotkey", …) stay in the log rather than being
// attributed to a module.
//
// Bus events arrive asynchronously, so `at` guards against a stale event
// overwriting fresher state: a queued error published before a successful retry
// must not resurrect itself afterwards.
func (a *App) recordRuntimeError(module, msg string, at time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, known := a.modules[module]; !known {
		return
	}
	if at.Before(a.errorAt[module]) {
		return // something more recent already set this module's state
	}
	a.lastError[module] = msg
	a.errorAt[module] = at
}

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
		// A module that fails Init never reaches Start, so its reason would
		// otherwise never reach the panel either.
		a.setError(m.ID(), a.safeCallErr(m.ID(), "Init", func() error {
			return m.Init(a.moduleContext(m))
		}))
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
		// The Windows build has no console (-H windowsgui), so record the reason:
		// it is the only place the panel (and therefore the user) can see it.
		a.setError(id, err)
		a.bus.Log(id, "error", fmt.Sprintf("启动失败: %v", err))
		return err
	}

	a.mu.Lock()
	a.state[id] = true
	a.mu.Unlock()
	a.clearError(id)

	a.bindHotkey(m)
	a.log.Info("模块已启动", "module", id)
	a.bus.State(id, map[string]any{"running": true})
	a.refreshTrayMenu()
	return nil
}

// Disable stops a module and unbinds its hotkey.
func (a *App) Disable(id string) error {
	m, ok := a.Module(id)
	if !ok {
		return fmt.Errorf("未找到模块 %s", id)
	}
	a.hotkeys.Unbind(id)
	// Unbinding clears the hotkey slot as well: nothing is registered any more,
	// so a stale "hotkey occupied" note would be misleading.
	a.setHotkeyError(id, "")

	stopErr := a.safeCallErr(id, "Stop", m.Stop)
	a.setError(id, stopErr)

	a.mu.Lock()
	a.state[id] = false
	a.mu.Unlock()

	a.log.Info("模块已停止", "module", id)
	a.bus.State(id, map[string]any{"running": false})
	a.refreshTrayMenu()
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
//
// core.HotkeyManager.Bind reports problems only through its warn callback, so
// the outcome is mirrored into the module's "hotkey" error slot here: an
// invalid or refused hotkey becomes visible as LastError while the module keeps
// running — a lost shortcut is a warning, not a stopped module.
func (a *App) bindHotkey(m core.Module) {
	id := m.ID()
	hk := a.cfgMgr.Module(id).Hotkey()
	_, ok, err := core.ParseHotkey(hk)
	switch {
	case hk == "":
		// Unbound on purpose: drop the old registration along with its note.
		a.hotkeys.Unbind(id)
		a.setHotkeyError(id, "")
	case err != nil || !ok:
		a.hotkeys.Unbind(id)
		a.setHotkeyError(id, fmt.Sprintf("热键 %s 无效: %v", hk, err))
	default:
		// Assume the binding will be refused and clear that again only once the
		// manager really owns the combo: Bind cannot return an error, so this is
		// the only way to catch a registration the OS rejected.
		a.setHotkeyError(id, hotkeyRefusedMsg(hk))
		a.hotkeys.Bind(id, hk, func() error {
			return a.safeCallErr(id, "OnHotkey", m.OnHotkey)
		})
		if _, bound := a.hotkeys.Combo(id); bound {
			a.setHotkeyError(id, "")
		}
	}
}

// hotkeyRefusedMsg explains a registration the manager did not take.
//
// Off Windows core has no global-hotkey backend at all, so blaming another
// program there would send the user hunting for a culprit that cannot exist.
func hotkeyRefusedMsg(hk string) string {
	if runtime.GOOS != "windows" {
		return fmt.Sprintf("热键 %s 未生效：当前平台不支持全局热键（仅 Windows）", hk)
	}
	return fmt.Sprintf("热键 %s 注册失败（可能被其它程序占用）", hk)
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
	err := a.safeCallErr(id, "OnHotkey", m.OnHotkey)
	a.setError(id, err)
	return err
}

// OpenUI opens a module's dedicated window.
func (a *App) OpenUI(id string) error {
	m, ok := a.Module(id)
	if !ok {
		return fmt.Errorf("未找到模块 %s", id)
	}
	err := a.safeCallErr(id, "OpenUI", m.OpenUI)
	a.setError(id, err)
	return err
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
			needsRestart = o.Restart
			break
		}
	}

	if err := a.safeCallErr(module, "ApplyOption", func() error {
		return m.ApplyOption(key, value)
	}); err != nil {
		// A rejected option is recorded but does not fail the request: the value
		// is already persisted and restartable options are retried below.
		a.setError(module, err)
		a.log.Warn("应用配置项失败", "module", module, "key", key, "err", err)
	} else {
		a.clearError(module)
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
	a.refreshTrayMenu()
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

	// Stop serving the panel first so no in-flight request can observe a
	// half-torn-down module registry.
	a.mu.Lock()
	p := a.panel
	a.panel = nil
	a.mu.Unlock()
	if p != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = p.Shutdown(ctx)
		cancel()
	}

	// Remove the icon before stopping modules so a crashed module cannot
	// leave a dead tray entry behind in the notification area.
	a.mu.Lock()
	t := a.tray
	a.tray = nil
	a.mu.Unlock()
	if t != nil {
		t.Destroy()
	}

	for _, m := range a.Modules() {
		if a.Running(m.ID()) {
			a.setError(m.ID(), a.safeCallErr(m.ID(), "Stop", m.Stop))
		}
	}
	a.cancel()
	a.bus.Close()

	// Release the message pump so main's Run() returns and the process exits.
	a.postQuit()

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

// Version implements core.AppControl: it exposes the running version to
// modules (the updater compares it against the latest release tag).
func (a *App) Version() string { return Version }

// PanelURL returns the preferences panel address.
func (a *App) PanelURL() string {
	a.panelURLMu.RLock()
	defer a.panelURLMu.RUnlock()
	return a.panelURL
}

// OpenPanel opens the preferences panel using the configured presentation.
//
// OpenInWebview is a preference rather than a hard requirement: true prefers
// the independent native window, while false prefers the system browser. If
// the preferred route fails, OpenPanel tries the other route so a missing
// WebView runtime or browser handler does not leave the user without a panel.
func (a *App) OpenPanel() error {
	preferNative := a.Config().App().OpenInWebview

	if preferNative && nativePanelAvailable() {
		nativeErr := showNativePanel()
		if nativeErr == nil {
			return nil
		}
		a.log.Warn("打开原生面板失败，改用浏览器", "err", nativeErr)

		browserErr := a.openPanelInBrowser()
		if browserErr == nil {
			return nil
		}
		a.log.Warn("浏览器打开面板失败", "err", browserErr)
		return fmt.Errorf("打开原生面板失败: %v；浏览器回退也失败: %w", nativeErr, browserErr)
	}

	if preferNative {
		a.log.Warn("原生面板不可用，改用浏览器")
	}
	browserErr := a.openPanelInBrowser()
	if browserErr == nil {
		return nil
	}
	a.log.Warn("浏览器打开面板失败，尝试原生窗口", "err", browserErr)

	// Availability may change while the Wails runtime starts, so check it at
	// fallback time rather than relying on the value observed above.
	if nativePanelAvailable() {
		nativeErr := showNativePanel()
		if nativeErr == nil {
			return nil
		}
		a.log.Warn("原生面板回退失败", "err", nativeErr)
		return fmt.Errorf("浏览器打开面板失败: %v；原生窗口回退也失败: %w", browserErr, nativeErr)
	}
	return browserErr
}

// openPanelInBrowser preserves the cross-platform browser launch path. The
// explicit platform launcher is retained as a fallback for systems where the
// shared URL handler is unavailable.
func (a *App) openPanelInBrowser() error {
	url := a.PanelURL()
	if url == "" {
		return fmt.Errorf("首选项服务尚未就绪")
	}
	openErr := sysutil.OpenURL(url)
	if openErr == nil {
		return nil
	}
	a.log.Warn("系统 URL 打开器失败，尝试平台浏览器启动器", "url", url, "err", openErr)

	var fallbackErr error
	switch runtime.GOOS {
	case "windows":
		fallbackErr = exec.Command("cmd", "/c", "start", "", url).Start()
	case "darwin":
		fallbackErr = exec.Command("open", url).Start()
	default:
		fallbackErr = exec.Command("xdg-open", url).Start()
	}
	if fallbackErr != nil {
		return fmt.Errorf("系统 URL 打开器失败: %v；平台浏览器启动器失败: %w", openErr, fallbackErr)
	}
	return nil
}

// StartPanel boots the preferences HTTP server and records its URL.
//
// The server listens on 127.0.0.1 only, and a failure to bind is logged
// rather than fatal: the tray and hotkeys must keep working regardless.
func (a *App) StartPanel() error {
	port := a.cfgMgr.Config().App.ServerPort
	s := server.New(panelProvider{a: a}, server.Options{
		Port: port,
		Host: "127.0.0.1",
		Log:  a.log,
	})

	url, err := s.Start()
	if err != nil {
		return err
	}

	a.mu.Lock()
	a.panel = s
	a.mu.Unlock()

	a.SetPanelURL(url)
	a.log.Info("首选项面板已启动", "url", url)

	// The tray menu greys out the panel entry until the URL exists.
	a.refreshTrayMenu()
	return nil
}

// StartTray creates the notification-area icon and installs its menu.
//
// On platforms without a native tray the returned tray is a no-op, so callers
// need no build tags; the panel remains the control surface there.
func (a *App) StartTray() error {
	t := a.currentTray()
	if t == nil {
		t = tray.New(a.log, tray.HandlerFunc(a.onTraySelect))
		a.mu.Lock()
		a.tray = t
		a.mu.Unlock()
	}

	a.refreshTrayMenu()
	return t.Show()
}

// currentTray returns the tray without holding a lock during menu rebuilds.
//
// refreshTrayMenu calls Modules/Running, which take the same mutex; reading
// the field through this helper avoids nesting RLock (which can deadlock
// when a writer is waiting between the two acquisitions).
func (a *App) currentTray() tray.Tray {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tray
}

// refreshTrayMenu rebuilds the tray menu from the live module state.
//
// It is called after every enable/disable/hotkey/option change so the check
// marks and the hotkey hints always match the running configuration.
func (a *App) refreshTrayMenu() {
	t := a.currentTray()
	if t == nil {
		return
	}

	items := []tray.Item{{
		ID:    "open_panel",
		Title: "打开首选项面板",
	}}
	if a.PanelURL() == "" {
		items[0].Disabled = true
	}
	items = append(items, tray.Item{Separator: true})

	for _, m := range a.Modules() {
		id := m.ID()
		hk := a.cfgMgr.Module(id).Hotkey()
		title := m.Name()
		if title == "" {
			title = id
		}
		if hk != "" {
			title += "  (" + hk + ")"
		}
		// A module that is switched off must stay clickable, otherwise there
		// is no way back on from the tray; Checked mirrors the live state.
		items = append(items, tray.Item{
			ID:        "toggle:" + id,
			Title:     title,
			Checkable: true,
			Checked:   a.Running(id) || a.cfgMgr.Module(id).Enabled(),
		})
	}

	// One "open" entry per module that exposes a window; modules without a UI
	// return a no-op from OpenUI, so a generic list stays correct as modules
	// are added.
	for _, m := range a.Modules() {
		title := m.Name()
		if title == "" {
			title = m.ID()
		}
		items = append(items, tray.Item{
			ID:    "openui:" + m.ID(),
			Title: "打开 " + title,
		})
	}

	items = append(items,
		tray.Item{ID: "autostart", Title: "开机自启", Checkable: true, Checked: a.cfgMgr.Config().App.Autostart},
		tray.Item{Separator: true},
		tray.Item{ID: "quit", Title: "退出"},
	)

	t.SetMenu(tray.Menu{
		Tooltip: "PCMannager",
		Items:   items,
	})
}

// onTraySelect dispatches a tray menu selection.
func (a *App) onTraySelect(id string) {
	switch {
	case id == "open_panel":
		if err := a.OpenPanel(); err != nil {
			a.log.Warn("打开面板失败", "err", err)
		}
	case id == "quit":
		a.ShutdownAsync()
	case id == "autostart":
		a.toggleAutostart()
	case strings.HasPrefix(id, "toggle:"):
		a.toggleFromTray(strings.TrimPrefix(id, "toggle:"))
	case strings.HasPrefix(id, "openui:"):
		mod := strings.TrimPrefix(id, "openui:")
		if err := a.OpenUI(mod); err != nil {
			a.log.Warn("打开模块界面失败", "module", mod, "err", err)
		}
	default:
		a.log.Warn("未知的托盘菜单项", "id", id)
	}
}

// toggleFromTray flips a module's persisted switch and starts/stops it.
func (a *App) toggleFromTray(id string) {
	on := !a.cfgMgr.Module(id).Enabled()
	if err := a.EnableModule(id, on); err != nil {
		a.log.Warn("切换模块失败", "module", id, "err", err)
		return
	}
	a.log.Info("模块开关已切换", "module", id, "enabled", on)
	a.bus.State(id, map[string]any{"enabled": on})
	a.refreshTrayMenu()
}

// toggleAutostart flips the persisted autostart setting and applies it.
func (a *App) toggleAutostart() {
	var on bool
	if err := a.cfgMgr.UpdateApp(func(cfg *config.App) {
		cfg.Autostart = !cfg.Autostart
		on = cfg.Autostart
	}); err != nil {
		a.log.Warn("保存开机自启设置失败", "err", err)
		return
	}
	if err := a.applyAutostart(on); err != nil {
		a.log.Warn("应用开机自启失败", "err", err)
	}
	a.refreshTrayMenu()
}

// SyncAutostart reconciles the OS registration with the persisted setting.
//
// It runs at boot so that a config edited on another machine (or by hand)
// takes effect without the user having to toggle the switch twice.
func (a *App) SyncAutostart() {
	want := a.cfgMgr.Config().App.Autostart
	if sysutil.IsAutostart(paths.AppName) == want {
		return // already in the requested state
	}
	if err := a.applyAutostart(want); err != nil {
		a.log.Warn("同步开机自启失败", "want", want, "err", err)
	}
}

// applyAutostart writes the OS-level autostart registration.
func (a *App) applyAutostart(on bool) error {
	if err := sysutil.SetAutostart(paths.AppName, on); err != nil {
		return err
	}
	a.bus.Log("app", "info", fmt.Sprintf("开机自启已%s", map[bool]string{true: "开启", false: "关闭"}[on]))
	return nil
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

// ModuleError reports the most recent failure recorded for a module: Start,
// Stop, Init, ApplyOption, OpenUI, OnHotkey, a runtime error published to the
// bus, or a hotkey that could not be registered. "" means no failure on record.
//
// It is the panel-facing half of the slots written by setError/setHotkeyError,
// and takes the lock itself rather than reading them inline so every read is
// consistent under a module toggling concurrently.
func (a *App) ModuleError(id string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return joinModuleError(a.lastError[id], a.hotkeyError[id])
}

// setError records why a module failed and clears the record when err is nil.
//
// It also stamps errorAt, so a bus event published *before* this point (and
// still in flight) cannot overwrite the newer state when it arrives.
func (a *App) setError(id string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.errorAt[id] = time.Now()
	if err == nil {
		delete(a.lastError, id)
		return
	}
	a.lastError[id] = err.Error()
}

// clearError drops a module's recorded failure after a successful operation.
func (a *App) clearError(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.lastError, id)
	a.errorAt[id] = time.Now()
}

// setHotkeyError records or clears a hotkey registration failure for a module.
//
// The hotkey slot is separate from setError on purpose: a failed registration
// is a warning that does not stop the module, so it must neither overwrite a
// Start failure nor be wiped when Start/Stop succeeds — and a later successful
// rebind must not erase that Start failure either.
func (a *App) setHotkeyError(id, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if msg == "" {
		delete(a.hotkeyError, id)
		return
	}
	a.hotkeyError[id] = msg
}

// joinModuleError merges the two error slots into one panel-facing string.
func joinModuleError(modErr, hotkeyErr string) string {
	switch {
	case modErr != "" && hotkeyErr != "":
		return modErr + "；" + hotkeyErr
	case modErr != "":
		return modErr
	default:
		return hotkeyErr
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
