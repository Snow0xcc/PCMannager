package screenshot

import (
	"image"
	"image/draw"
)

// Scroll-stitching parameters.
const (
	// scrollSearchRows is how far down the new frame the previous frame's bottom
	// strip is searched for. It must exceed the largest scroll step (a wheel
	// notch at typical text size), and staying modest keeps the search cheap.
	scrollSearchRows = 400

	// scrollMatchRows is the height of the strip taken from the previous frame
	// and located in the new one. A taller strip is more distinctive (fewer
	// false matches) but slower; 40 rows of text or UI is already very unlikely
	// to repeat exactly.
	scrollMatchRows = 40

	// scrollTolerance is the per-channel difference that still counts as a match.
	// Screen captures of static content are pixel-exact, so this only absorbs
	// incidental rounding (re-rendered anti-aliased text, a blinking caret).
	// Keeping it small is what stops gradient backgrounds from matching at wrong
	// offsets.
	scrollTolerance = 4

	// scrollMinAdvance is the smallest overlap shift treated as real scrolling.
	// Below it, the frame is probably a repaint of the same position.
	scrollMinAdvance = 3
)

// stitchResult describes the outcome of appending one frame.
type stitchResult struct {
	// Stitched is the accumulated tall image after this frame.
	Stitched *image.RGBA
	// Advanced is how many pixels of new content were appended (0 = no motion).
	Advanced int
	// Matched reports whether the previous frame's strip was located, i.e. the
	// two frames genuinely overlap. False means stitching is no longer reliable
	// (the user scrolled too far, or content changed).
	Matched bool
}

// scrollStitcher accumulates a long screenshot from successive scrolled frames.
//
// The algorithm is the standard one: keep the bottom strip of the last frame,
// find where that strip reappears in the new frame, and append only the rows
// below it. Matching on pixels is what lets it work on arbitrary content without
// knowing anything about the window being captured.
type scrollStitcher struct {
	base     *image.RGBA
	strip    *image.RGBA
	stripTop int
	width    int
}

// newScrollStitcher seeds the stitcher with the first captured frame.
func newScrollStitcher(first *image.RGBA) *scrollStitcher {
	if first == nil {
		return nil
	}
	b := first.Bounds()
	s := &scrollStitcher{
		base:  cloneRGBA(first),
		width: b.Dx(),
	}
	s.captureStrip()
	return s
}

// captureStrip remembers the bottom strip of the current image for the next
// comparison.
func (s *scrollStitcher) captureStrip() {
	h := s.base.Bounds().Dy()
	stripH := scrollMatchRows
	if h < stripH {
		stripH = h
	}
	top := h - stripH
	strip := image.NewRGBA(image.Rect(0, 0, s.width, stripH))
	draw.Draw(strip, strip.Bounds(), s.base, image.Pt(0, top), draw.Src)
	s.strip = strip
	s.stripTop = top
}

// append matches next against the remembered strip and grows the stitched image.
func (s *scrollStitcher) append(next *image.RGBA) stitchResult {
	if s == nil || next == nil {
		return stitchResult{Stitched: s.image()}
	}
	b := next.Bounds()
	if b.Dx() != s.width {
		// Width changed (the captured window was resized); stitching is no
		// longer meaningful, so report it rather than producing garbage.
		return stitchResult{Stitched: s.image(), Matched: false}
	}

	offset, ok := findStripOffset(next, s.strip, s.stripTop)
	if !ok {
		// No overlap found: the user scrolled past a screenful, or the content
		// changed. The honest response is to stop rather than duplicate rows.
		return stitchResult{Stitched: s.image(), Matched: false}
	}

	// The strip was taken from s.stripTop in the OLD image; its new position is
	// `offset`. New content is everything below it in the new frame.
	newRows := b.Dy() - (offset + s.strip.Bounds().Dy())
	if newRows < scrollMinAdvance {
		// Same position (or a tiny jitter): nothing new to add.
		return stitchResult{Stitched: s.image(), Matched: true}
	}

	grown := image.NewRGBA(image.Rect(0, 0, s.width, s.base.Bounds().Dy()+newRows))
	draw.Draw(grown, s.base.Bounds(), s.base, image.Point{}, draw.Src)
	dstTop := s.base.Bounds().Dy()
	srcTop := offset + s.strip.Bounds().Dy()
	draw.Draw(grown,
		image.Rect(0, dstTop, s.width, dstTop+newRows),
		next,
		image.Pt(b.Min.X, b.Min.Y+srcTop),
		draw.Src)

	s.base = grown
	s.captureStrip()
	return stitchResult{Stitched: s.image(), Advanced: newRows, Matched: true}
}

// image returns the stitched image, or nil for a nil stitcher.
func (s *scrollStitcher) image() *image.RGBA {
	if s == nil {
		return nil
	}
	return s.base
}

// height reports the current stitched height.
func (s *scrollStitcher) height() int {
	if s == nil || s.base == nil {
		return 0
	}
	return s.base.Bounds().Dy()
}

// findStripOffset locates strip inside img, given where the strip sat in the
// previous frame (expected).
//
// Search starts AT expected and radiates outwards (preferring a downward move,
// i.e. a smaller y, because scrolling down moves the strip up the frame).
// Starting from the expected position is what disambiguates uniform content: a
// solid background or repeated table rows can match at many offsets, and only
// the proximity to the previous position identifies the real one. A plain
// top-down scan would pick the first coincidental match and stitch the wrong
// rows.
func findStripOffset(img, strip *image.RGBA, expected int) (int, bool) {
	ib := img.Bounds()
	sb := strip.Bounds()
	if sb.Dy() <= 0 || ib.Dy() < sb.Dy() || ib.Dx() < sb.Dx() {
		return 0, false
	}
	limit := ib.Dy() - sb.Dy()

	if expected > limit {
		expected = limit
	}
	if expected < 0 {
		expected = 0
	}

	// Probe offsets in order of increasing distance from expected, preferring
	// smaller y at equal distance (scroll-down is the common case).
	maxDist := limit - expected
	if expected > maxDist {
		maxDist = expected
	}
	if maxDist > scrollSearchRows {
		maxDist = scrollSearchRows
	}
	for d := 0; d <= maxDist; d++ {
		if cand := expected - d; cand >= 0 && stripsMatch(img, strip, cand) {
			return cand, true
		}
		if d > 0 {
			if cand := expected + d; cand <= limit && stripsMatch(img, strip, cand) {
				return cand, true
			}
		}
	}
	return 0, false
}

// stripsMatch reports whether strip appears in img at the given y offset.
//
// Rows are sampled with a stride rather than compared exhaustively: a full
// per-pixel comparison of a wide strip over hundreds of candidate offsets is
// needlessly slow, and sampling every row plus a column stride still makes a
// false positive on real UI content vanishingly unlikely.
func stripsMatch(img, strip *image.RGBA, off int) bool {
	sb := strip.Bounds()
	for y := 0; y < sb.Dy(); y++ {
		iy := img.Bounds().Min.Y + off + y
		if iy >= img.Bounds().Max.Y {
			return false
		}
		srow := strip.Pix[y*strip.Stride : y*strip.Stride+sb.Dx()*4]
		irow := img.Pix[(iy-img.Bounds().Min.Y)*img.Stride:]
		if !rowMatches(srow, irow, sb.Dx()) {
			return false
		}
	}
	return true
}

// rowMatches compares two RGBA rows pixel by pixel within scrollTolerance.
func rowMatches(a, b []byte, width int) bool {
	for x := 0; x < width; x++ {
		o := x * 4
		if o+3 >= len(a) || o+3 >= len(b) {
			return false
		}
		if diffByte(a[o], b[o]) > scrollTolerance ||
			diffByte(a[o+1], b[o+1]) > scrollTolerance ||
			diffByte(a[o+2], b[o+2]) > scrollTolerance {
			return false
		}
	}
	return true
}

// diffByte returns the absolute difference of two bytes.
func diffByte(a, b byte) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// cloneRGBA returns a copy of img so callers never alias the capture buffer.
func cloneRGBA(img *image.RGBA) *image.RGBA {
	if img == nil {
		return nil
	}
	out := image.NewRGBA(img.Bounds())
	copy(out.Pix, img.Pix)
	return out
}
