//go:build !windows

package clipboard

import (
	"errors"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// sendPaste is unavailable outside Windows: no synthetic input API is
// reachable without cgo, so the module leaves pasting to the user.
//
// The target window is ignored (there is no way to focus it here); the error
// is returned so the caller can tell the user to press Ctrl+V manually.
func sendPaste(target winui.HWND) error { return errors.New("自动粘贴仅 Windows 支持") }
