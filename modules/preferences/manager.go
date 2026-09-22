package preferences

import "github.com/snow0xcc/pcmannager/internal/core"

// Manager is a thin wrapper exposing the shared App to the preferences UI.
// It embeds the core manager so feature open/run helpers remain available.
type Manager struct {
	*core.Manager
	app *core.App
}

// NewManager wraps a core.Manager for the preferences panel.
func NewManager(m *core.Manager) *Manager {
	return &Manager{Manager: m, app: m.App()}
}
