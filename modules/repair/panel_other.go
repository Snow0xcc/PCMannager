//go:build !windows

package pcrepair

// openPanel is a no-op on non-Windows platforms.
func openPanel(f *Feature) {
	f.app.Log.Infof("pcrepair panel not available")
}
