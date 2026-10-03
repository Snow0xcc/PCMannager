package screenshot

import (
	"fmt"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"io"
	"sync"
)

// Recording bounds. GIF has no inter-frame compression worth the name, so its
// size grows with frames × pixels; these caps keep a runaway recording from
// exhausting memory. They are deliberately generous for the common case (a
// short clip of a window) and the cap is surfaced through the module state.
const (
	recMinFPS     = 5
	recMaxFPS     = 25
	recDefaultFPS = 10

	// recMaxFrames caps how many frames are buffered. At the default 10fps this
	// is about two minutes; a GIF that long is already very large, and this is
	// the point where the recorder stops rather than growing without bound.
	recMaxFrames = 1200
)

// gifDelayUnit converts a frame interval into GIF's centisecond delay.
const gifDelayUnit = 100

// frameEncoder accumulates captured frames and writes them as one animated GIF.
//
// Frames are stored quantised to a SHARED palette (palette.Plan9). That is what
// keeps memory bounded: a paletted frame is one byte per pixel instead of four,
// and reusing one palette across frames keeps the GIF delta-friendly rather than
// storing a fresh palette per frame.
type frameEncoder struct {
	mu     sync.Mutex
	bounds image.Rectangle
	frames []*image.Paletted
	delay  int
	fps    int
}

// newFrameEncoder prepares an encoder for a w×h region captured at fps.
func newFrameEncoder(w, h, fps int) *frameEncoder {
	if fps < recMinFPS {
		fps = recMinFPS
	}
	if fps > recMaxFPS {
		fps = recMaxFPS
	}
	if w <= 0 || h <= 0 {
		return nil
	}
	delay := gifDelayUnit / fps
	if delay < 2 {
		// GIF treats a delay below 2cs as "as fast as possible", which viewers
		// then clamp unpredictably; 2cs is the fastest sane value.
		delay = 2
	}
	return &frameEncoder{
		bounds: image.Rect(0, 0, w, h),
		delay:  delay,
		fps:    fps,
	}
}

// add quantises and stores one frame.
//
// It returns false when the frame cap was reached, so the caller can stop the
// capture loop instead of recording into the void.
func (e *frameEncoder) add(img *image.RGBA) bool {
	if e == nil || img == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.frames) >= recMaxFrames {
		return false
	}

	pm := image.NewPaletted(e.bounds, palette.Plan9)
	// Nearest-colour mapping without dithering: screen content is mostly flat,
	// and dithering would add a crawling noise pattern between frames.
	draw.Draw(pm, e.bounds, img, img.Bounds().Min, draw.Src)
	e.frames = append(e.frames, pm)
	return true
}

// count reports how many frames have been buffered.
func (e *frameEncoder) count() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.frames)
}

// capped reports whether the frame cap has been reached.
func (e *frameEncoder) capped() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.frames) >= recMaxFrames
}

// encode writes the buffered frames as an animated GIF.
func (e *frameEncoder) encode(w io.Writer) error {
	if e == nil {
		return fmt.Errorf("screenshot: 没有可编码的帧")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.frames) == 0 {
		return fmt.Errorf("screenshot: 没有可编码的帧")
	}
	delays := make([]int, len(e.frames))
	for i := range delays {
		delays[i] = e.delay
	}
	doc := &gif.GIF{
		Image:     e.frames,
		Delay:     delays,
		LoopCount: 0, // loop forever
	}
	return gif.EncodeAll(w, doc)
}
