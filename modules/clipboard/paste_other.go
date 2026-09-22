//go:build !windows

package clipboard

import "errors"

// sendPaste is unavailable outside Windows: no synthetic input API is
// reachable without cgo, so the module leaves pasting to the user.
func sendPaste() error { return errors.New("自动粘贴仅 Windows 支持") }
