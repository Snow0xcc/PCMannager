// Package server exposes GoBox's preferences panel as a local HTTP service.
//
// It is deliberately free of Win32/CGO dependencies so the panel works on every
// platform: a JSON REST API for configuration, a Server-Sent Events stream for
// live module events and an embedded single-page frontend.
//
// The HTTP layer never reaches into the module registry directly; it talks to a
// Provider implemented by internal/app. That keeps the dependency direction
// app -> server (never server -> app), so the two packages cannot form a cycle.
package server

import "github.com/snow0xcc/pcmannager/internal/core"

// ModuleInfo is the panel-facing snapshot of one module.
type ModuleInfo struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Enabled     bool          `json:"enabled"`
	Running     bool          `json:"running"`
	Hotkey      string        `json:"hotkey"`
	Options     []core.Option `json:"options"`
	Actions     []core.Action `json:"actions"`
	State       core.State    `json:"state"`
}

// ModulePatch is a partial update of a module's settings.
type ModulePatch struct {
	Enabled *bool          `json:"enabled,omitempty"`
	Hotkey  *string        `json:"hotkey,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

// AppConfig is the panel-facing view of the application settings.
type AppConfig struct {
	Autostart     bool   `json:"autostart"`
	Theme         string `json:"theme"`
	LogLevel      string `json:"log_level"`
	DataDir       string `json:"data_dir"`
	ServerPort    int    `json:"server_port"`
	OpenInWebview bool   `json:"open_in_webview"`
	Language      string `json:"language"`
	// EffectiveDataDir is the path the app actually resolved and is using,
	// after merging the user override with the OS default. It is read-only:
	// the user override flows through DataDir, and this field is for display
	// so the panel never shows an empty box while data lives elsewhere.
	EffectiveDataDir string `json:"effective_data_dir"`
}

// AppConfigPatch is a partial update of the application settings.
type AppConfigPatch struct {
	Autostart     *bool   `json:"autostart,omitempty"`
	Theme         *string `json:"theme,omitempty"`
	LogLevel      *string `json:"log_level,omitempty"`
	DataDir       *string `json:"data_dir,omitempty"`
	ServerPort    *int    `json:"server_port,omitempty"`
	OpenInWebview *bool   `json:"open_in_webview,omitempty"`
	Language      *string `json:"language,omitempty"`
}

// Provider is the application-side data source used by the HTTP handlers.
type Provider interface {
	// Modules returns every registered module in declaration order.
	Modules() []ModuleInfo
	// Module returns one module snapshot.
	Module(id string) (ModuleInfo, bool)
	// PatchModule applies an incremental settings change.
	PatchModule(id string, patch ModulePatch) error
	// RunAction triggers a declared module action.
	RunAction(module, action string, params map[string]string) error
	// RunHotkey invokes a module's hotkey handler.
	RunHotkey(id string) error
	// OpenUI opens a module's dedicated window.
	OpenUI(id string) error

	// AppConfig returns the application settings.
	AppConfig() AppConfig
	// PatchAppConfig applies an incremental settings change.
	PatchAppConfig(patch AppConfigPatch) error

	// Capabilities describes the platform features available in this build.
	Capabilities() any
	// ValidateHotkey checks a hotkey string, returning a reason when invalid.
	ValidateHotkey(hotkey string) error

	// Subscribe returns the live event stream and an unsubscribe function.
	Subscribe() (<-chan core.Event, func())

	// Version is the application version string.
	Version() string
}
