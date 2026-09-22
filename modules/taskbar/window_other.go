//go:build !windows

package statusbar

// window is a no-op on non-Windows platforms. The statusbar feature still
// collects and logs stats; only the on-screen tray widget is unavailable.
type window struct{}

// newWindow builds a (no-op) window for non-Windows builds.
func newWindow(cfg *statusbarCfg) *window { return &window{} }

// SetStats is a no-op outside Windows.
func (w *window) SetStats(s Stats) {}

// Run is a no-op outside Windows.
func (w *window) Run() {}

// Stop is a no-op outside Windows.
func (w *window) Stop() {}
