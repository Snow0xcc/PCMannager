//go:build windows

package selfcontext

import "github.com/gonutz/w32"

// ActiveTitle returns the title of the currently focused window.
func ActiveTitle() (string, error) {
	hwnd := w32.GetForegroundWindow()
	if hwnd == 0 {
		return "", nil
	}
	return w32.GetWindowText(hwnd), nil
}
