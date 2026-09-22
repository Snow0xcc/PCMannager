//go:build !windows

package clipboard

import "fmt"

// showViewer is a no-op outside Windows, where no native window can be shown.
//
// The history itself is platform independent, so the module stays usable: the
// panel still lists the entries and can write any of them back.
func showViewer(f *Feature) error {
	if f == nil || f.ctx == nil {
		return errNoFeature
	}
	f.ctx.Logger.Info("剪贴板历史窗口在非 Windows 平台不可用", "module", moduleID,
		"count", len(f.hist.All()))
	f.ctx.Bus.Log(moduleID, "info", "剪贴板历史窗口仅在 Windows 下可用")
	return nil
}

// singleLine is the portable counterpart of the Windows row helper, kept so
// the module's state previews look the same on every platform.
func singleLine(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		out = append(out, r)
	}
	return string(out)
}

// previewText mirrors the Windows preview pane for callers that render the
// entry through the panel instead of a native window.
func previewText(e Entry) string {
	if e.Kind == KindImage {
		return fmt.Sprintf("[图片 PNG，%d 字节]", len(e.Data))
	}
	return e.Text
}
