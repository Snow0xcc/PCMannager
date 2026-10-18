//go:build windows

package screenshot

import (
	"image"

	"github.com/kbinani/screenshot"
)

// captureDisplayCount reports how many displays can be captured.
func captureDisplayCount() int { return screenshot.NumActiveDisplays() }

// grabDisplay captures the full primary display for the region editor; the
// caller must have checked captureDisplayCount first.
//
// The kbinani dependency (and, through it, xgb/shm/dbus on Linux and the cgo
// CoreGraphics path on macOS) is confined to this file so non-Windows builds
// keep a slim binary; see capture_other.go.
func grabDisplay() (image.Rectangle, *image.RGBA, error) {
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	return bounds, img, err
}

// grabRegion captures an arbitrary screen-space rectangle (recording and
// scrolling-capture frame sampling).
func grabRegion(r image.Rectangle) (*image.RGBA, error) {
	return screenshot.CaptureRect(r)
}

// primaryDisplayHeight reports the primary display height in pixels, 0 when
// unknown. It bounds the scrolling-capture stitched height.
func primaryDisplayHeight() int {
	if screenshot.NumActiveDisplays() <= 0 {
		return 0
	}
	return screenshot.GetDisplayBounds(0).Dy()
}
