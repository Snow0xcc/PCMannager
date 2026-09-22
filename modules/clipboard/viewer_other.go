//go:build !windows

package clipboard

import "github.com/snow0xcc/pcmannager/internal/core"

// showViewer is a no-op outside Windows, where wui cannot render a window.
func showViewer(hist *History, app *core.App, writeBack func(string)) {
	app.Log.Infof("clipboard viewer not available on this platform")
}
