//go:build !windows

package preferences

// Show is a no-op outside Windows, where walk cannot render a window.
func Show(mgr *Manager) {
	mgr.app.Log.Infof("preferences panel not available on this platform")
}
