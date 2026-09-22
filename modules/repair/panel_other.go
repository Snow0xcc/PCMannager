//go:build !windows

package repair

// openPanel is a no-op on non-Windows platforms: walk cannot render a window
// and every repair command targets Windows.
func openPanel(f *Feature) {
	if f != nil && f.ctx != nil && f.ctx.Logger != nil {
		f.ctx.Logger.Warn("repair 面板仅支持 Windows", "module", moduleID)
	}
}

// focusPanel has no window to focus off Windows.
func (f *Feature) focusPanel() {}

// closePanel has no window to close off Windows.
func (f *Feature) closePanel() {}
