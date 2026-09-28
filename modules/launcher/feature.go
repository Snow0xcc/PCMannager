// Package launcher implements the uTools/dtools-style global quick panel:
// Alt+Space pops a centred, borderless, always-on-top search box that can
// trigger module actions and open URLs, hiding itself the moment it loses
// focus.
//
// Why Alt+Space was never implemented before: the hotkey framework
// (internal/core.HotkeyManager) always supported it — `space` is in the VK
// table — but no module ever bound it. Alt+Space is also the system menu
// mnemonic (DefWindowProc → WM_SYSCOMMAND/SC_KEYMENU), and full-screen
// applications or tools like PowerToys Run may already own the combo, so
// registration can genuinely fail. The design therefore treats the hotkey as
// best-effort: a failed registration is surfaced as the module's hotkey error
// (the panel shows it) while the panel itself stays reachable through the
// tray/panel actions.
package launcher

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/snow0xcc/pcmannager/internal/core"
)

const (
	moduleID = "launcher"

	// optHotkey is the panel hotkey. It defaults to the system menu mnemonic
	// (Alt+Space); users whose desktop already owns that combo can rebind it
	// in the panel rather than lose the feature.
	optHotkey    = "hotkey"
	optMaxResult = "max_results"
)

const (
	defaultHotkey    = "alt+space"
	defaultMaxResult = 8
)

// errNoFeature is returned by methods that run before Init.
var errNoFeature = errors.New("launcher: 模块未初始化")

// Feature implements the quick-launcher module.
type Feature struct {
	core.Base

	ctx *core.Context

	mu      sync.Mutex
	running bool
	// panel is the native window layer; nil off Windows.
	panel *panelState
	// commands is the searchable action table (rebuilt on Start).
	commands []command
}

// NewFeature constructs the launcher module.
func NewFeature() core.Module { return &Feature{} }

// Compile-time proof of the module contract.
var _ core.Module = (*Feature)(nil)

// ID implements core.Module.
func (f *Feature) ID() string { return moduleID }

// Name implements core.Module.
func (f *Feature) Name() string { return "快捷面板" }

// Description implements core.Module.
func (f *Feature) Description() string {
	return "全局快捷面板（Alt+Space 唤起）：搜索并执行模块动作、打开网站；失焦自动隐藏"
}

// Options implements core.Module.
func (f *Feature) Options() []core.Option {
	return []core.Option{
		{Key: optHotkey, Label: "唤起热键", Kind: core.KindString, Default: defaultHotkey,
			Help: "默认 Alt+Space（系统菜单键）；被占用时可改为 alt+q 等"},
		{Key: optMaxResult, Label: "候选数量", Kind: core.KindInt,
			Default: defaultMaxResult, Min: 3, Max: 20, Step: 1,
			Help: "搜索结果列表最多显示的条数"},
	}
}

// Actions implements core.Module.
func (f *Feature) Actions() []core.Action {
	return []core.Action{
		{ID: "open_panel", Label: "打开快捷面板", Kind: core.ActionOpen,
			Description: "立即弹出快捷面板（等同按下热键）"},
	}
}

// Init implements core.Module.
func (f *Feature) Init(ctx *core.Context) error {
	if ctx == nil {
		return errNoFeature
	}
	f.ctx = ctx
	return nil
}

// Start implements core.Module: it builds the command table and prepares the
// native panel. The hotkey itself is bound by the app through the module's
// declared hotkey config, exactly like every other module.
func (f *Feature) Start() error {
	if f.ctx == nil {
		return errNoFeature
	}
	if !f.ctx.Config.Enabled() {
		return nil
	}

	f.mu.Lock()
	if !f.running {
		f.commands = buildCommands(f.ctx)
	}
	f.running = true
	f.mu.Unlock()

	f.ctx.Logger.Info("快捷面板已就绪", "module", moduleID,
		"hotkey", f.ctx.Config.Hotkey(), "commands", len(f.commands))
	f.ctx.Bus.State(moduleID, f.State())
	return nil
}

// Stop implements core.Module: the panel window is destroyed so the hotkey
// toggling a fresh instance later does not resurrect stale state.
func (f *Feature) Stop() error {
	f.mu.Lock()
	wasRunning := f.running
	f.running = false
	p := f.panel
	f.mu.Unlock()

	if wasRunning && p != nil {
		p.close()
	}
	if wasRunning && f.ctx != nil {
		f.ctx.Logger.Info("快捷面板已停止", "module", moduleID)
		f.ctx.Bus.State(moduleID, f.State())
	}
	return nil
}

// State implements core.Module.
func (f *Feature) State() core.State {
	f.mu.Lock()
	running := f.running
	visible := f.panel != nil && f.panel.isVisible()
	f.mu.Unlock()

	return core.State{
		"running":     running,
		"visible":     visible,
		"hotkey":      f.hotkey(),
		"max_results": f.maxResults(),
		"commands":    len(f.commands),
	}
}

// OnHotkey implements core.Module: toggle the panel.
func (f *Feature) OnHotkey() error { return f.OpenUI() }

// OpenUI implements core.Module: pop the panel (or hide it when already up).
func (f *Feature) OpenUI() error {
	if f.ctx == nil {
		return errNoFeature
	}
	f.mu.Lock()
	p := f.panel
	running := f.running
	f.mu.Unlock()

	if !running {
		return fmt.Errorf("launcher: 模块未运行")
	}
	if p != nil && p.isVisible() {
		p.hide()
		return nil
	}
	return f.showPanel()
}

// showPanel creates (once) and shows the native panel on its own thread.
func (f *Feature) showPanel() error {
	f.mu.Lock()
	p := f.panel
	cmds := f.commands
	max := f.maxResults()
	f.mu.Unlock()

	if p == nil {
		p = newPanel(f.ctx, f)
		f.mu.Lock()
		f.panel = p
		f.mu.Unlock()
	}
	p.present(cmds, max)
	return nil
}

// RunAction executes a declared panel action.
func (f *Feature) RunAction(id string, params map[string]string) error {
	switch id {
	case "open_panel":
		return f.OpenUI()
	}
	return fmt.Errorf("launcher: 未知动作 %s", id)
}

// ApplyOption validates settings.
func (f *Feature) ApplyOption(key string, value any) error {
	if f.ctx == nil {
		return errNoFeature
	}
	switch key {
	case optHotkey:
		// 热键格式由 app 的 bindHotkey 统一解析与报错，这里只接受字符串。
		if _, ok := value.(string); !ok {
			return fmt.Errorf("launcher: %s 需要字符串", key)
		}
	case optMaxResult:
		if n, ok := toInt(value); !ok || n < 3 || n > 20 {
			return fmt.Errorf("launcher: %s 需要 3-20 的整数", key)
		}
	default:
		return fmt.Errorf("launcher: 未知配置项 %s", key)
	}
	f.ctx.Bus.State(moduleID, f.State())
	return nil
}

// hotkey reads the configured hotkey string.
func (f *Feature) hotkey() string {
	if f.ctx == nil {
		return defaultHotkey
	}
	s, _ := f.ctx.Config.Get(optHotkey, defaultHotkey).(string)
	if strings.TrimSpace(s) == "" {
		return defaultHotkey
	}
	return s
}

// maxResults reads the configured candidate count.
func (f *Feature) maxResults() int {
	if f.ctx == nil {
		return defaultMaxResult
	}
	if n, ok := toInt(f.ctx.Config.Get(optMaxResult, defaultMaxResult)); ok && n >= 3 && n <= 20 {
		return n
	}
	return defaultMaxResult
}

// toInt narrows numeric config shapes (same as the other modules).
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}
