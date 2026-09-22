// Package preferences renders the local settings window. On Windows it uses
// walk; elsewhere Show is a no-op so the rest of the app keeps building.
//
// The panel never touches the config document directly: all reads and writes
// go through *app.App, which owns the config.Manager and the module registry.
package preferences

import (
	"log/slog"

	"github.com/snow0xcc/pcmannager/internal/app"
)

// Manager is a small façade over *app.App giving the preferences UI typed
// access to modules and configuration without reaching into internals.
type Manager struct {
	app *app.App
}

// NewManager wraps the running application for the preferences panel.
func NewManager(a *app.App) *Manager {
	return &Manager{app: a}
}

// App returns the wrapped application (nil when constructed with nil).
func (m *Manager) App() *app.App {
	if m == nil {
		return nil
	}
	return m.app
}

// Log returns the application logger, or nil when unavailable.
func (m *Manager) Log() *slog.Logger {
	if m == nil || m.app == nil {
		return nil
	}
	return m.app.Log()
}

// ModuleIDs lists every module known to the application, in registration order
// first, then any module that only exists in the configuration file.
func (m *Manager) ModuleIDs() []string {
	if m == nil || m.app == nil {
		return nil
	}
	seen := map[string]bool{}
	ids := make([]string, 0, 8)
	for _, mod := range m.app.Modules() {
		id := mod.ID()
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	cfg := m.app.Config()
	if cfg == nil {
		return ids
	}
	for _, id := range cfg.ModuleIDs() {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}
