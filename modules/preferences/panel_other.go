//go:build !windows

package preferences

// Show is a no-op outside Windows, where walk cannot render a window.
func Show(mgr *Manager) {
	if log := mgr.Log(); log != nil {
		log.Warn("preferences 面板仅支持 Windows")
	}
}
