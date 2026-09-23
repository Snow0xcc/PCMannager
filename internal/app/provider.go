package app

import (
	"errors"
	"fmt"

	"github.com/snow0xcc/pcmannager/internal/config"
	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/server"
	"github.com/snow0xcc/pcmannager/internal/winui"
)

// Version is the application version reported by the panel and the API.
const Version = "0.1.0"

// actionRunner is implemented by modules that expose user-triggerable actions.
// It is optional: a module without actions simply skips that panel affordance.
type actionRunner interface {
	RunAction(id string, params map[string]string) error
}

// PanelProvider returns the panel data source for non-HTTP consumers.
//
// The native window (internal/wailsapp) needs the same data contract the HTTP
// handlers use. Exposing it here — rather than letting wailsapp reach into the
// app's internals — keeps one source of truth for panel data and preserves the
// app -> server / app -> wailsapp dependency direction.
func (a *App) PanelProvider() server.Provider { return panelProvider{a: a} }

// panelProvider adapts *App to the server.Provider interface.
//
// It lives here rather than in internal/server so the dependency direction
// stays app -> server; the HTTP layer never reaches into the app's internals.
type panelProvider struct{ a *App }

// Modules returns every registered module as a panel-facing snapshot.
func (p panelProvider) Modules() []server.ModuleInfo {
	mods := p.a.Modules()
	out := make([]server.ModuleInfo, 0, len(mods))
	for _, m := range mods {
		out = append(out, p.moduleInfo(m))
	}
	return out
}

// Module returns one module snapshot.
func (p panelProvider) Module(id string) (server.ModuleInfo, bool) {
	m, ok := p.a.Module(id)
	if !ok {
		return server.ModuleInfo{}, false
	}
	return p.moduleInfo(m), true
}

// moduleInfo builds a panel snapshot for one module. The module's runtime
// State() is merged with the live option values from the configuration so the
// panel can echo back the currently persisted value of every option (otherwise
// the form would always show the declared default, regardless of saved state).
func (p panelProvider) moduleInfo(m core.Module) server.ModuleInfo {
	id := m.ID()
	cfg := p.a.Config().Module(id)
	state := p.safeState(m)
	if _, present := state["options"]; !present {
		state["options"] = cfg.Options()
	}
	return server.ModuleInfo{
		ID:          id,
		Name:        m.Name(),
		Description: m.Description(),
		Enabled:     cfg.Enabled(),
		Running:     p.a.Running(id),
		Hotkey:      cfg.Hotkey(),
		Options:     m.Options(),
		Actions:     m.Actions(),
		State:       state,
	}
}

// safeState guards against a State() implementation that panics.
func (p panelProvider) safeState(m core.Module) core.State {
	var s core.State
	_ = p.a.safeCallErr(m.ID(), "State", func() error {
		s = m.State()
		return nil
	})
	if s == nil {
		s = core.State{}
	}
	return s
}

// PatchModule applies an incremental settings change from the panel.
func (p panelProvider) PatchModule(id string, patch server.ModulePatch) error {
	if _, ok := p.a.Module(id); !ok {
		return fmt.Errorf("未找到模块 %s", id)
	}
	return p.a.SetModuleSettings(id, patch.Enabled, patch.Hotkey, patch.Options)
}

// RunAction triggers a declared module action.
func (p panelProvider) RunAction(module, action string, params map[string]string) error {
	m, ok := p.a.Module(module)
	if !ok {
		return fmt.Errorf("未找到模块 %s", module)
	}
	runner, ok := m.(actionRunner)
	if !ok {
		return fmt.Errorf("模块 %s 不支持操作", module)
	}
	if params == nil {
		params = map[string]string{}
	}
	return p.a.safeCallErr(module, "RunAction", func() error {
		return runner.RunAction(action, params)
	})
}

// RunHotkey invokes a module's hotkey handler.
func (p panelProvider) RunHotkey(id string) error { return p.a.RunHotkey(id) }

// OpenUI opens a module's dedicated window.
func (p panelProvider) OpenUI(id string) error { return p.a.OpenUI(id) }

// AppConfig returns the application settings.
func (p panelProvider) AppConfig() server.AppConfig {
	c := p.a.Config().App()
	return server.AppConfig{
		Autostart:        c.Autostart,
		Theme:            c.Theme,
		LogLevel:         c.LogLevel,
		DataDir:          c.DataDir,
		ServerPort:       c.ServerPort,
		OpenInWebview:    c.OpenInWebview,
		Language:         c.Language,
		EffectiveDataDir: p.a.DataDir(),
	}
}

// PatchAppConfig applies an incremental application settings change.
func (p panelProvider) PatchAppConfig(patch server.AppConfigPatch) error {
	if patch.Autostart != nil {
		if err := p.a.applyAutostart(*patch.Autostart); err != nil {
			return err
		}
	}
	return p.a.Config().UpdateApp(func(c *config.App) {
		if patch.Autostart != nil {
			c.Autostart = *patch.Autostart
		}
		if patch.Theme != nil {
			c.Theme = *patch.Theme
		}
		if patch.LogLevel != nil {
			c.LogLevel = *patch.LogLevel
		}
		if patch.DataDir != nil {
			c.DataDir = *patch.DataDir
		}
		if patch.ServerPort != nil {
			c.ServerPort = *patch.ServerPort
		}
		if patch.OpenInWebview != nil {
			c.OpenInWebview = *patch.OpenInWebview
		}
		if patch.Language != nil {
			c.Language = *patch.Language
		}
	})
}

// Capabilities describes the platform features available in this build.
func (p panelProvider) Capabilities() any { return winui.Capabilities() }

// ValidateHotkey checks a hotkey string, returning a reason when invalid.
func (p panelProvider) ValidateHotkey(hotkey string) error {
	if hotkey == "" {
		return nil // empty means "unbound"
	}
	if !core.ValidHotkey(hotkey) {
		return errors.New("热键格式无效，应为 mod+mod+key，例如 ctrl+alt+f1")
	}
	return nil
}

// Subscribe returns the live event stream and an unsubscribe function.
func (p panelProvider) Subscribe() (<-chan core.Event, func()) {
	return p.a.Bus().Subscribe()
}

// Version is the application version string.
func (p panelProvider) Version() string { return Version }
