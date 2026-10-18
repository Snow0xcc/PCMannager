//go:build !windows

package screenshot

import (
	"errors"
	"image"
)

// errCaptureUnsupported reflects the platform matrix: the region editor that
// consumes captured frames is Windows-only, so the capture backend is compiled
// out of non-Windows builds (see capture_windows.go).
var errCaptureUnsupported = errors.New("屏幕捕获当前仅支持 Windows")

// captureDisplayCount reports that no display can be captured on platforms
// without a capture backend. It must return a compile-time constant: captureAs
// tests `captureDisplayCount() <= 0` and the constant lets the compiler prove
// the editor/record/scroll tree unreachable, so the GIF/MP4/scroll machinery
// is dead-code-eliminated from non-Windows binaries (mirrors the kbinani
// darwin stub this seam replaces).
func captureDisplayCount() int { return 0 }

// grabDisplay always fails on platforms without a capture backend.
func grabDisplay() (image.Rectangle, *image.RGBA, error) {
	return image.Rectangle{}, nil, errCaptureUnsupported
}

// grabRegion always fails on platforms without a capture backend.
func grabRegion(image.Rectangle) (*image.RGBA, error) {
	return nil, errCaptureUnsupported
}

// primaryDisplayHeight is unknown without a capture backend.
func primaryDisplayHeight() int { return 0 }
