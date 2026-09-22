//go:build !windows

package selfcontext

// showContext is a no-op outside Windows, where no native window can be shown.
//
// The recorder itself is platform independent, so the module stays usable: the
// panel still shows the timeline and the export/copy actions still work.
func showContext(f *Feature) error {
	if f == nil || f.ctx == nil {
		return errNoFeature
	}
	f.ctx.Logger.Info("上下文记录窗口在非 Windows 平台不可用", "module", moduleID,
		"count", len(f.Snapshot()))
	f.ctx.Bus.Log(moduleID, "info", "上下文记录窗口仅在 Windows 下可用")
	return nil
}
