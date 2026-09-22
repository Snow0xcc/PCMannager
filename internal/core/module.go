// Package core defines the contracts shared by every GoBox module.
//
// A module is a self-contained capability (taskbar stats, clipboard history,
// screenshot, …) that can be toggled, configured and bound to a global hotkey
// at runtime. Modules never talk to each other directly; they only see the
// Context handed to them by the application.
package core

import (
	"context"
	"log/slog"
)

// OptionKind drives which widget the preferences panel renders for an option.
type OptionKind string

const (
	KindBool   OptionKind = "bool"
	KindInt    OptionKind = "int"
	KindString OptionKind = "string"
	KindSelect OptionKind = "select"
	KindColor  OptionKind = "color"
)

// Option is a declarative configuration field. The web panel builds its form
// from these descriptors, so adding a setting never requires frontend changes.
type Option struct {
	Key     string     `json:"key"`
	Label   string     `json:"label"`
	Kind    OptionKind `json:"kind"`
	Default any        `json:"default,omitempty"`
	Min     int        `json:"min,omitempty"`
	Max     int        `json:"max,omitempty"`
	Step    int        `json:"step,omitempty"`
	Choices []Choice   `json:"choices,omitempty"`
	Help    string     `json:"help,omitempty"`
	// VisibleIn keeps the option out of the default form when it only makes
	// sense for a specific value of another option.
	VisibleIf *VisibleIf `json:"visible_if,omitempty"`
}

// Choice is one entry of a KindSelect option.
type Choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// VisibleIf expresses a simple dependency between options.
type VisibleIf struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// ActionKind tells the panel how to present an action button.
type ActionKind string

const (
	ActionNormal ActionKind = "normal"
	ActionDanger ActionKind = "danger"
	ActionInstall ActionKind = "install"
	ActionOpen   ActionKind = "open"
)

// Action is a declarative, user-triggerable operation. Install/cleanup style
// actions surface progress through ProgressFunc.
type Action struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Group       string     `json:"group,omitempty"`
	Description string     `json:"description,omitempty"`
	Kind        ActionKind `json:"kind,omitempty"`
	// Confirm forces a二次确认 dialog before running (dangerous operations).
	Confirm bool `json:"confirm,omitempty"`
	// Params are {{placeholder}} definitions the user must fill in first.
	Params []Param `json:"params,omitempty"`
	// Admin marks actions that need elevation.
	Admin bool `json:"admin,omitempty"`
}

// Param is a user-supplied substitution for an action template.
type Param struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Default     string `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// State is an arbitrary module-defined snapshot surfaced in the panel.
type State map[string]any

// Module is implemented by every capability in GoBox.
type Module interface {
	// ID is the stable machine identifier used in config, hotkeys and the API.
	ID() string
	// Name is the human readable module name.
	Name() string
	// Description is a one-line summary for the panel.
	Description() string
	// Options declares the module's settings.
	Options() []Option
	// Actions declares the module's buttons.
	Actions() []Action
	// Init receives the shared context. Called once, before Start.
	Init(ctx *Context) error
	// Start activates the module. Called on enable and at boot.
	Start() error
	// Stop deactivates the module. Must be idempotent.
	Stop() error
	// State reports the current runtime status for the panel.
	State() State
	// OnHotkey handles the module's global hotkey. May be nil-safe no-op.
	OnHotkey() error
	// OpenUI opens the module's dedicated window, if it has one.
	OpenUI() error
	// ApplyOption applies a runtime option change. Modules that persist all
	// options themselves may ignore this.
	ApplyOption(key string, value any) error
}

// Base provides no-op defaults so modules only implement what they need.
// Embed it to stay forward compatible when the interface grows.
type Base struct{}

func (Base) Options() []Option            { return nil }
func (Base) Actions() []Action            { return nil }
func (Base) State() State                 { return State{} }
func (Base) OnHotkey() error              { return nil }
func (Base) OpenUI() error                { return nil }
func (Base) ApplyOption(string, any) error { return nil }

// Context is the shared runtime handed to every module.
type Context struct {
	// Ctx is cancelled when the application shuts down.
	Ctx context.Context
	// Logger is the shared structured logger.
	Logger *slog.Logger
	// Bus broadcasts events to the panel and other subscribers.
	Bus *Bus
	// Config gives read/write access to this module's own settings.
	Config ModuleConfig
	// App exposes application-level controls (shutdown, restart, panel URL).
	App AppControl
	// DataDir is the per-module writable directory, already created.
	DataDir string
}

// ModuleConfig is the module-scoped view of the global configuration.
type ModuleConfig interface {
	// Enabled reports whether the module is switched on.
	Enabled() bool
	// Hotkey returns the configured hotkey string ("" = none).
	Hotkey() string
	// Get reads an option value, falling back to def when unset.
	Get(key string, def any) any
	// Set writes an option value and persists the configuration.
	Set(key string, value any) error
	// SetEnabled toggles the module and persists the configuration.
	SetEnabled(bool) error
	// SetHotkey updates the hotkey and persists the configuration.
	SetHotkey(string) error
}

// AppControl exposes application lifecycle to modules.
type AppControl interface {
	// Shutdown exits the application.
	Shutdown()
	// PanelURL returns the local preferences panel address.
	PanelURL() string
	// OpenPanel opens the preferences panel in the default browser.
	OpenPanel() error
	// EnableModule toggles another module by id.
	EnableModule(id string, on bool) error
}
