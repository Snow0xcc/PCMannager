// Package winui provides the thin, cgo-free Win32 surface GoBox needs:
// window creation, taskbar embedding, GDI text rendering, DPI awareness,
// tray icons and shell notifications.
//
// Everything here is guarded by build tags. The Windows implementation uses
// syscall.LazyDLL directly (no cgo) so `CGO_ENABLED=0 GOOS=windows go build`
// stays the single supported release command (PRD GFR-11).
package winui

// Rect is a Win32 RECT in device pixels.
type Rect struct {
	Left, Top, Right, Bottom int32
}

// Width returns the rectangle width.
func (r Rect) Width() int32 { return r.Right - r.Left }

// Height returns the rectangle height.
func (r Rect) Height() int32 { return r.Bottom - r.Top }

// HWND is a window handle.
type HWND uintptr

// Invalid is the null handle value.
const Invalid HWND = 0

// Valid reports whether the handle is usable.
func (h HWND) Valid() bool { return h != 0 }

// CapabilitiesInfo describes which native features the current build offers.
// The preferences panel shows this so users understand platform differences.
type CapabilitiesInfo struct {
	Native           bool `json:"native"`
	TaskbarEmbedding bool `json:"taskbar_embedding"`
	TrayIcon         bool `json:"tray_icon"`
	Toasts           bool `json:"toasts"`
	Elevation        bool `json:"elevation"`
	Hotkeys          bool `json:"hotkeys"`
}

// Capabilities reports platform feature availability.
func Capabilities() CapabilitiesInfo { return platformCapabilities() }
