// Package repair implements one-click Windows repair commands and software
// installation. It is part of every build; the interactive panel only renders
// on Windows (walk) and degrades to a no-op elsewhere.
package repair

import (
	"sync"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// moduleID mirrors the directory name; the registry, the config file and the
// hotkey bindings all look this module up by that id.
const moduleID = "repair"

// Feature implements the PC repair / tool installation module.
type Feature struct {
	core.Base

	ctx *core.Context

	mu       sync.Mutex
	running  bool
	panelRef bool // true while a panel window owns the UI
}

// NewFeature constructs the repair module.
func NewFeature() core.Module { return &Feature{} }

func (f *Feature) ID() string   { return moduleID }
func (f *Feature) Name() string { return "电脑修复与工具安装" }
func (f *Feature) Description() string {
	return "系统修复、网络排查、清理与软件一键安装"
}

// Options mirrors internal/config defaults for the repair module.
func (f *Feature) Options() []core.Option {
	return []core.Option{
		{Key: "confirm_danger", Label: "危险操作前二次确认", Kind: core.KindBool,
			Default: true, Help: "执行风险命令前弹出确认对话框"},
		{Key: "prefer_source", Label: "安装源", Kind: core.KindSelect,
			Default: "auto",
			Choices: []core.Choice{
				{Value: "auto", Label: "自动选择"},
				{Value: "winget", Label: "winget"},
				{Value: "choco", Label: "chocolatey"},
			},
			Help: "一键安装软件时优先使用的包管理器"},
	}
}

// Actions exposes the repair panel entry point plus a few safe one-click fixes.
func (f *Feature) Actions() []core.Action {
	return []core.Action{
		{ID: "open_panel", Label: "打开修复面板", Kind: core.ActionOpen,
			Description: "打开带分类按钮的修复/安装面板"},
		{ID: "flush_dns", Label: "刷新 DNS", Kind: core.ActionNormal,
			Group: "网络", Description: "ipconfig /flushdns"},
		{ID: "open_msconfig", Label: "打开系统设置", Kind: core.ActionOpen,
			Group: "系统", Description: "start ms-settings:"},
	}
}

// Init receives the shared application context.
func (f *Feature) Init(ctx *core.Context) error {
	f.ctx = ctx
	return nil
}

// RunAction executes a declared action. Unknown ids are reported as errors so
// the panel can surface typos instead of silently doing nothing.
func (f *Feature) RunAction(id string, params map[string]string) error {
	switch id {
	case "open_panel":
		return f.OpenUI()
	case "flush_dns":
		return f.Run("刷新 DNS", "ipconfig /flushdns")
	case "open_msconfig":
		return f.Run("打开系统设置", "start ms-settings:")
	default:
		return &unknownActionError{id: id}
	}
}

func (f *Feature) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running {
		return nil
	}
	if f.ctx != nil && f.ctx.Logger != nil {
		f.ctx.Logger.Info("repair 模块已启动", "module", moduleID)
		f.ctx.Bus.Log(moduleID, "info", "repair 模块已启动")
	}
	f.running = true
	return nil
}

// Stop is idempotent: repeated calls must never panic.
func (f *Feature) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.running {
		return nil
	}
	f.running = false
	f.closePanel()
	if f.ctx != nil && f.ctx.Logger != nil {
		f.ctx.Logger.Info("repair 模块已停止", "module", moduleID)
		f.ctx.Bus.Log(moduleID, "info", "repair 模块已停止")
	}
	return nil
}

// State reports whether the module (and its panel) are live, plus the
// effective install source so the panel can reflect stored options.
func (f *Feature) State() core.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := core.State{"running": f.running, "panel": f.panelRef}
	if src := f.preferSource(); src != "" {
		state["source"] = src
	}
	return state
}

// OnHotkey opens the repair/tool panel.
func (f *Feature) OnHotkey() error { return f.OpenUI() }

// OpenUI shows the repair/tool panel on a goroutine so the caller never blocks.
func (f *Feature) OpenUI() error {
	f.mu.Lock()
	if f.panelRef {
		f.mu.Unlock()
		f.focusPanel()
		return nil
	}
	f.panelRef = true
	f.mu.Unlock()

	go func() {
		defer func() {
			f.mu.Lock()
			f.panelRef = false
			f.mu.Unlock()
		}()
		openPanel(f)
	}()
	return nil
}

// ApplyOption reacts to a runtime option change. Both repair options are read
// lazily, so nothing has to be persisted here -- the app already saved them.
func (f *Feature) ApplyOption(key string, value any) error {
	if f.ctx == nil {
		return nil
	}
	if f.ctx.Logger != nil {
		f.ctx.Logger.Info("repair 配置已更新", "module", moduleID, "key", key, "value", value)
	}
	if f.ctx.Bus != nil {
		f.ctx.Bus.State(moduleID, f.State())
	}
	return nil
}

// Run executes one repair command line, logging the outcome to slog + bus.
func (f *Feature) Run(name, cmdline string) error {
	out, err := runCommand(cmdline)
	if f.ctx != nil {
		if f.ctx.Logger != nil {
			if err != nil {
				f.ctx.Logger.Error("repair 命令失败", "module", moduleID, "action", name, "err", err)
			} else {
				f.ctx.Logger.Info("repair 命令完成", "module", moduleID, "action", name)
			}
		}
		if f.ctx.Bus != nil {
			f.ctx.Bus.Progress(moduleID, name, 100, out)
		}
	}
	return err
}

// confirmDanger reports whether dangerous actions must be confirmed first.
func (f *Feature) confirmDanger() bool {
	if f.ctx == nil || f.ctx.Config == nil {
		return true
	}
	v, _ := f.ctx.Config.Get("confirm_danger", true).(bool)
	return v
}

// preferSource reports the configured package manager preference.
func (f *Feature) preferSource() string {
	if f.ctx == nil || f.ctx.Config == nil {
		return "auto"
	}
	s, _ := f.ctx.Config.Get("prefer_source", "auto").(string)
	if s == "" {
		return "auto"
	}
	return s
}

// unknownActionError is returned for action ids the module does not declare.
type unknownActionError struct{ id string }

func (e *unknownActionError) Error() string { return "未知操作: " + e.id }
