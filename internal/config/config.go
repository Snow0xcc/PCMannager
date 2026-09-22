// Package config loads, validates and atomically persists GoBox's YAML
// configuration.
//
// Layout follows the PRD (§7 配置总表):
//
//	app:
//	  autostart: false
//	  theme: auto
//	  log_level: info
//	  data_dir: ""
//	  server_port: 0
//	  language: zh-CN
//	modules:
//	  taskbar: { enabled: true, hotkey: "Ctrl+Alt+T", options: {...} }
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

// App holds application-wide settings.
type App struct {
	Autostart     bool   `yaml:"autostart" json:"autostart"`
	Theme         string `yaml:"theme" json:"theme"`
	LogLevel      string `yaml:"log_level" json:"log_level"`
	DataDir       string `yaml:"data_dir" json:"data_dir"`
	ServerPort    int    `yaml:"server_port" json:"server_port"`
	OpenInWebview bool   `yaml:"open_in_webview" json:"open_in_webview"`
	Language      string `yaml:"language" json:"language"`
}

// Module is the per-module persisted configuration.
type Module struct {
	Enabled bool           `yaml:"enabled" json:"enabled"`
	Hotkey  string         `yaml:"hotkey" json:"hotkey"`
	Options map[string]any `yaml:"options,omitempty" json:"options,omitempty"`
}

// Config is the root configuration document.
type Config struct {
	App     App               `yaml:"app" json:"app"`
	Modules map[string]Module `yaml:"modules" json:"modules"`

	mu   sync.RWMutex
	path string
	// defaults remembers each module's declared default options so Get can
	// fall back without the module repeating itself everywhere.
	defaults map[string]map[string]any
}

// Default returns the shipped default configuration (PRD §7).
//
// selfcontext is deliberately OFF: it records screen content, so this
// privacy-sensitive module is opt-in (PRD SC-09).
func Default() *Config {
	return &Config{
		App: App{
			Autostart:  false,
			Theme:      "auto",
			LogLevel:   "info",
			DataDir:    "",
			ServerPort: 0,
			Language:   "zh-CN",
		},
		Modules: map[string]Module{
			"taskbar": {
				Enabled: true,
				Hotkey:  "Ctrl+Alt+T",
				Options: map[string]any{
					"interval":      1000,
					"show_download": true,
					"show_upload":   true,
					"show_cpu":      false,
					"show_mem":      false,
					"show_disk":     false,
					"show_uptime":   false,
					"align":         "right",
					"offset_x":      8,
					"margin_top":    0,
					"margin_v":      0,
					"layout":        "two-line",
					"num_align":     "left",
					"speed_unit":    "B",
					"unit_space":    true,
					"font_family":   "Microsoft YaHei",
					"font_size":     9,
					"fg_color":      "#FFFFFF",
					"bg_mode":       "theme",
					"bg_color":      "#1E1E1E",
					"follow_theme":  true,
					"separator":     "space",
					"render":        "gdi",
					"avoid_widgets": true,
					"multi_monitor": false,
				},
			},
			"clipboard": {
				Enabled: true,
				Hotkey:  "Ctrl+`",
				Options: map[string]any{
					"max_items":      500,
					"store_images":   true,
					"paste_on_copy":  false,
					"retention_days": 30,
				},
			},
			"selfcontext": {
				Enabled: false, // privacy: opt-in
				Hotkey:  "Ctrl+Alt+M",
				Options: map[string]any{
					"interval":       300,
					"retention_days": 7,
					"capture_mode":   "primary",
					"pause_on_lock":  true,
				},
			},
			"screenshot": {
				Enabled: true,
				Hotkey:  "F1",
				Options: map[string]any{
					"format":      "png",
					"jpg_quality": 90,
					"copy_after":  true,
					"save_dir":    "",
					"max_history": 200,
				},
			},
			"repair": {
				Enabled: true,
				Hotkey:  "",
				Options: map[string]any{
					"prefer_source":  "auto",
					"confirm_danger": true,
				},
			},
		},
		defaults: map[string]map[string]any{},
	}
}

// Manager owns the config file and serializes access to it.
type Manager struct {
	cfg  *Config
	path string
}

// Load reads config.yaml from dir, creating it with defaults when missing.
// Decoding happens into the default document so unknown/new keys survive.
func Load(dir string) (*Manager, error) {
	cfg := Default()
	path := filepath.Join(dir, "config.yaml")

	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取 %s 失败: %w", path, err)
	}

	if cfg.Modules == nil {
		cfg.Modules = map[string]Module{}
	}
	// Backfill modules introduced by a newer build, preserving user values.
	for id, def := range Default().Modules {
		if cur, ok := cfg.Modules[id]; ok {
			merged := def
			merged.Enabled = cur.Enabled
			if cur.Hotkey != "" || def.Hotkey == "" {
				merged.Hotkey = cur.Hotkey
			}
			merged.Options = mergeOptions(def.Options, cur.Options)
			cfg.Modules[id] = merged
			continue
		}
		cfg.Modules[id] = def
	}
	cfg.path = path

	m := &Manager{cfg: cfg, path: path}
	if err := m.Save(); err != nil { // materialize the normalized file
		return nil, err
	}
	return m, nil
}

// mergeOptions overlays stored options on top of declared defaults.
func mergeOptions(def, cur map[string]any) map[string]any {
	out := make(map[string]any, len(def)+len(cur))
	for k, v := range def {
		out[k] = v
	}
	for k, v := range cur {
		out[k] = v
	}
	return out
}

// Config returns the live configuration document.
func (m *Manager) Config() *Config { return m.cfg }

// Path returns the config file location.
func (m *Manager) Path() string { return m.path }

// Save writes the configuration atomically (tmp file + rename).
func (m *Manager) Save() error {
	m.cfg.mu.RLock()
	data, err := yaml.Marshal(m.cfg)
	m.cfg.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", tmp, err)
	}
	if err := os.Rename(tmp, m.path); err != nil {
		// Windows can refuse rename while the target is momentarily locked;
		// fall back to a direct write so the user's change is not lost.
		if werr := os.WriteFile(m.path, data, 0o644); werr != nil {
			return fmt.Errorf("重命名 %s 失败: %w", m.path, err)
		}
	}
	return nil
}

// DeclareDefaults registers module option defaults so Get can resolve values
// that were never persisted and the panel can display them.
func (m *Manager) DeclareDefaults(module string, opts map[string]any) {
	m.cfg.mu.Lock()
	defer m.cfg.mu.Unlock()
	if m.cfg.defaults == nil {
		m.cfg.defaults = map[string]map[string]any{}
	}
	if _, ok := m.cfg.defaults[module]; !ok {
		m.cfg.defaults[module] = map[string]any{}
	}
	for k, v := range opts {
		m.cfg.defaults[module][k] = v
	}
}

// Module returns a live, module-scoped view of the configuration.
func (m *Manager) Module(id string) *ModuleView {
	return &ModuleView{mgr: m, id: id}
}

// ModuleView is the module-scoped configuration accessor.
type ModuleView struct {
	mgr *Manager
	id  string
}

// Enabled reports whether the module is switched on.
func (v *ModuleView) Enabled() bool {
	v.mgr.cfg.mu.RLock()
	defer v.mgr.cfg.mu.RUnlock()
	return v.mgr.cfg.Modules[v.id].Enabled
}

// Hotkey returns the configured hotkey ("" = none).
func (v *ModuleView) Hotkey() string {
	v.mgr.cfg.mu.RLock()
	defer v.mgr.cfg.mu.RUnlock()
	return v.mgr.cfg.Modules[v.id].Hotkey
}

// Get reads an option value, falling back to declared default then def.
func (v *ModuleView) Get(key string, def any) any {
	v.mgr.cfg.mu.RLock()
	defer v.mgr.cfg.mu.RUnlock()
	if mod, ok := v.mgr.cfg.Modules[v.id]; ok && mod.Options != nil {
		if got, ok := mod.Options[key]; ok && got != nil {
			return got
		}
	}
	if d, ok := v.mgr.cfg.defaults[v.id]; ok {
		if got, ok := d[key]; ok {
			return got
		}
	}
	return def
}

// Set writes an option value and persists the configuration.
func (v *ModuleView) Set(key string, value any) error {
	v.mgr.cfg.mu.Lock()
	mod := v.mgr.cfg.Modules[v.id]
	if mod.Options == nil {
		mod.Options = map[string]any{}
	}
	mod.Options[key] = value
	v.mgr.cfg.Modules[v.id] = mod
	v.mgr.cfg.mu.Unlock()
	return v.mgr.Save()
}

// SetEnabled toggles the module and persists the configuration.
func (v *ModuleView) SetEnabled(on bool) error {
	v.mgr.cfg.mu.Lock()
	mod := v.mgr.cfg.Modules[v.id]
	mod.Enabled = on
	v.mgr.cfg.Modules[v.id] = mod
	v.mgr.cfg.mu.Unlock()
	return v.mgr.Save()
}

// SetHotkey updates the hotkey and persists the configuration.
func (v *ModuleView) SetHotkey(hk string) error {
	v.mgr.cfg.mu.Lock()
	mod := v.mgr.cfg.Modules[v.id]
	mod.Hotkey = hk
	v.mgr.cfg.Modules[v.id] = mod
	v.mgr.cfg.mu.Unlock()
	return v.mgr.Save()
}

// Options returns a copy of the module's persisted options.
func (v *ModuleView) Options() map[string]any {
	v.mgr.cfg.mu.RLock()
	defer v.mgr.cfg.mu.RUnlock()
	out := map[string]any{}
	for k, val := range v.mgr.cfg.Modules[v.id].Options {
		out[k] = val
	}
	return out
}

// App returns a snapshot of the app settings.
func (m *Manager) App() App {
	m.cfg.mu.RLock()
	defer m.cfg.mu.RUnlock()
	return m.cfg.App
}

// ModuleIDs returns configured module ids sorted alphabetically.
func (m *Manager) ModuleIDs() []string {
	m.cfg.mu.RLock()
	defer m.cfg.mu.RUnlock()
	out := make([]string, 0, len(m.cfg.Modules))
	for id := range m.cfg.Modules {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// UpdateApp mutates the app section and persists the configuration.
func (m *Manager) UpdateApp(fn func(*App)) error {
	m.cfg.mu.Lock()
	fn(&m.cfg.App)
	m.cfg.mu.Unlock()
	return m.Save()
}
