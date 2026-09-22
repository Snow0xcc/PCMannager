//go:build !windows

package screenshot

import "image"

// openEditor is a no-op outside Windows, where walk cannot render a window.
func openEditor(app interface{}, img *image.RGBA, bounds image.Rectangle, onSave, onCopy func(image.Image)) {
}
