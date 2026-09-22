//go:build !windows

package selfcontext

// showContext is a no-op outside Windows.
func showContext(f *Feature) {
	f.app.Log.Infof("selfcontext viewer not available on this platform")
}
