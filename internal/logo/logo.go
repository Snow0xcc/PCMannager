// Package logo renders the application's brand mark: a slender blue butterfly.
//
// There is deliberately no SVG parser here (a renderer would pull a dependency
// into a CGO_ENABLED=0 build that is kept dependency-light), so the mark is
// drawn procedurally with Go's image primitives — which also means it scales to
// any pixel size without resampling artefacts: tray icons need 16×16 and 32×32,
// window icons 32×32 and 256×256, and each is rasterised fresh from geometry.
package logo

import (
	"image"
	"image/color"
	"math"
)

// Brand colours. The butterfly is a calm steel blue (素雅蓝), with the body in
// a darker shade so it reads against both light and dark taskbars.
var (
	// wingBlue is the dominant wing fill. Straight alpha, RGBA order.
	wingBlue = color.RGBA{R: 0x4f, G: 0x8c, B: 0xff, A: 0xff} // #4F8CFF
	// wingDeep shades the lower wings for a hint of depth.
	wingDeep = color.RGBA{R: 0x2f, G: 0x62, B: 0xd6, A: 0xff} // #2F62D6
	// bodyDark is the torso/antennae colour.
	bodyDark = color.RGBA{R: 0x1c, G: 0x3a, B: 0x70, A: 0xff} // #1C3A70
)

// antennaWidth is the stroke weight of the antennae in normalised units. It
// lives at package scope because the SVG exporter (svg.go) strokes the same
// curves with the same pen as Render.
const antennaWidth = 2.2

// pt is a 2D point in a normalised 0..100 coordinate space; the drawing scales
// to the requested output size, so the geometry is authored once at any size.
type pt struct{ x, y float64 }

// bezier evaluates a cubic Bézier at t.
func bezier(p0, p1, p2, p3 pt, t float64) pt {
	u := 1 - t
	a := u * u * u
	b := 3 * u * u * t
	c := 3 * u * t * t
	d := t * t * t
	return pt{
		x: a*p0.x + b*p1.x + c*p2.x + d*p3.x,
		y: a*p0.y + b*p1.y + c*p2.y + d*p3.y,
	}
}

// bezierPath samples a chain of cubic segments into a polygon.
func bezierPath(segs [][4]pt, samples int) []pt {
	var out []pt
	for _, s := range segs {
		for i := 0; i < samples; i++ {
			out = append(out, bezier(s[0], s[1], s[2], s[3], float64(i)/float64(samples)))
		}
	}
	return out
}

// upperWing is one side's upper wing: a broad petal sweeping up-left from the
// body (the right side is the mirror image).
var upperWing = [][4]pt{
	// From the thorax, out along the top edge...
	{{x: 50, y: 46}, {x: 34, y: 26}, {x: 14, y: 18}, {x: 10, y: 34}},
	// ...round the far tip...
	{{x: 10, y: 34}, {x: 6, y: 50}, {x: 22, y: 56}, {x: 40, y: 52}},
	// ...and back to the body along the lower edge.
	{{x: 40, y: 52}, {x: 46, y: 50}, {x: 49, y: 48}, {x: 50, y: 46}},
}

// lowerWing is one side's lower wing: a smaller, rounder petal below.
var lowerWing = [][4]pt{
	{{x: 50, y: 52}, {x: 40, y: 60}, {x: 26, y: 72}, {x: 36, y: 84}},
	{{x: 36, y: 84}, {x: 44, y: 94}, {x: 58, y: 82}, {x: 56, y: 62}},
	{{x: 56, y: 62}, {x: 55, y: 56}, {x: 52, y: 53}, {x: 50, y: 52}},
}

// bodyPath is the slender fuselage: a narrow lens from head to tail.
var bodyPath = [][4]pt{
	{{x: 50, y: 38}, {x: 53, y: 50}, {x: 53, y: 66}, {x: 50, y: 82}},
	{{x: 50, y: 82}, {x: 47, y: 66}, {x: 47, y: 50}, {x: 50, y: 38}},
}

// wingSpot is the small white dot on each upper wing — the "素雅" accent that
// keeps the mark recognisable at 16px, where shading alone disappears.
func wingSpot() (pt, float64) { return pt{x: 24, y: 38}, 5 }

// antenna is one side's antenna: a thin curve from the head up-outward.
func antenna(left bool) [][4]pt {
	sign := 1.0
	x0 := 51.0
	if left {
		sign = -1.0
		x0 = 49.0
	}
	return [][4]pt{{
		{x: x0, y: 40}, {x: x0 + sign*2, y: 30}, {x: x0 + sign*8, y: 22}, {x: x0 + sign*12, y: 18},
	}}
}

// rasteriser fills polygons over a normalised 0..100 canvas.
type rasteriser struct {
	w, h  int
	scale float64
	img   *image.RGBA
}

// newRasteriser prepares a square output of side s.
func newRasteriser(s int) *rasteriser {
	return &rasteriser{
		w: s, h: s,
		scale: float64(s) / 100.0,
		img:   image.NewRGBA(image.Rect(0, 0, s, s)),
	}
}

// toPx maps a normalised point to pixel coordinates.
func (r *rasteriser) toPx(p pt) (float64, float64) {
	return p.x * r.scale, p.y * r.scale
}

// fillPolygon paints a polygon with a scanline fill (even-odd rule). Coverage
// is computed per pixel centre, which is plenty for icon-sized geometry and
// needs no anti-aliasing dependency.
func (r *rasteriser) fillPolygon(poly []pt, c color.RGBA) {
	if len(poly) < 3 || r.w == 0 {
		return
	}
	px := make([]float64, len(poly))
	py := make([]float64, len(poly))
	minY, maxY := math.Inf(1), math.Inf(-1)
	for i, p := range poly {
		px[i], py[i] = r.toPx(p)
		if py[i] < minY {
			minY = py[i]
		}
		if py[i] > maxY {
			maxY = py[i]
		}
	}
	y0 := int(math.Floor(minY))
	y1 := int(math.Ceil(maxY))
	if y0 < 0 {
		y0 = 0
	}
	if y1 > r.h {
		y1 = r.h
	}
	for y := y0; y < y1; y++ {
		// Pixel-centre scanline.
		sy := float64(y) + 0.5
		xs := make([]float64, 0, 8)
		for i := 0; i < len(poly); i++ {
			j := (i + 1) % len(poly)
			x1, x2 := px[i], px[j]
			ya, yb := py[i], py[j]
			if (ya <= sy && yb > sy) || (yb <= sy && ya > sy) {
				t := (sy - ya) / (yb - ya)
				xs = append(xs, x1+t*(x2-x1))
			}
		}
		// Even-odd fill: sort crossings, fill between pairs.
		for i := 0; i < len(xs); i++ {
			for j := i + 1; j < len(xs); j++ {
				if xs[j] < xs[i] {
					xs[i], xs[j] = xs[j], xs[i]
				}
			}
		}
		for i := 0; i+1 < len(xs); i += 2 {
			sx0 := int(math.Ceil(xs[i] - 0.5))
			sx1 := int(math.Floor(xs[i+1] - 0.5))
			if sx0 < 0 {
				sx0 = 0
			}
			if sx1 > r.w-1 {
				sx1 = r.w - 1
			}
			for x := sx0; x <= sx1; x++ {
				r.img.SetRGBA(x, y, c)
			}
		}
	}
}

// strokePolyline draws a polyline with a round pen of the given width.
func (r *rasteriser) strokePolyline(pts []pt, width float64, c color.RGBA) {
	if len(pts) < 2 {
		return
	}
	half := width * r.scale / 2
	for i := 0; i+1 < len(pts); i++ {
		x1, y1 := r.toPx(pts[i])
		x2, y2 := r.toPx(pts[i+1])
		r.strokeSegment(x1, y1, x2, y2, half, c)
	}
}

// strokeSegment stamps a thick line via distance-to-segment tests.
func (r *rasteriser) strokeSegment(x1, y1, x2, y2, half float64, c color.RGBA) {
	minX := math.Min(x1, x2) - half
	maxX := math.Max(x1, x2) + half
	minY := math.Min(y1, y2) - half
	maxY := math.Max(y1, y2) + half

	// Segment length; degenerate segments are drawn as dots.
	dx, dy := x2-x1, y2-y1
	length := math.Hypot(dx, dy)

	x0 := int(math.Floor(minX))
	xN := int(math.Ceil(maxX))
	y0 := int(math.Floor(minY))
	yN := int(math.Ceil(maxY))
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if xN > r.w {
		xN = r.w
	}
	if yN > r.h {
		yN = r.h
	}
	for y := y0; y < yN; y++ {
		for x := x0; x < xN; x++ {
			cx := float64(x) + 0.5
			cy := float64(y) + 0.5
			if length < 1e-6 {
				if math.Hypot(cx-x1, cy-y1) <= half {
					r.img.SetRGBA(x, y, c)
				}
				continue
			}
			// Distance from (cx,cy) to the segment.
			t := ((cx-x1)*dx + (cy-y1)*dy) / (length * length)
			if t < 0 {
				t = 0
			}
			if t > 1 {
				t = 1
			}
			px := x1 + t*dx
			py := y1 + t*dy
			if math.Hypot(cx-px, cy-py) <= half {
				r.img.SetRGBA(x, y, c)
			}
		}
	}
}

// fillCircle paints a filled disc.
func (r *rasteriser) fillCircle(center pt, rad float64, c color.RGBA) {
	cx, cy := r.toPx(center)
	radPx := rad * r.scale
	x0 := int(cx - radPx - 1)
	xN := int(math.Ceil(cx + radPx + 1))
	y0 := int(cy - radPx - 1)
	yN := int(math.Ceil(cy + radPx + 1))
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if xN > r.w {
		xN = r.w
	}
	if yN > r.h {
		yN = r.h
	}
	for y := y0; y < yN; y++ {
		for x := x0; x < xN; x++ {
			if math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) <= radPx {
				r.img.SetRGBA(x, y, c)
			}
		}
	}
}

// mirrorX folds the x axis of the 0..100 space around 50, so the left-side wing
// geometry also yields the right side without hand-authoring both.
func mirrorX(poly []pt) []pt {
	out := make([]pt, len(poly))
	for i, p := range poly {
		out[i] = pt{x: 100 - p.x, y: p.y}
	}
	return out
}

// Render draws the butterfly at the requested square size and returns it.
//
// size should be an icon-friendly value (16/32/48/64/256); any positive value
// works, the geometry simply scales.
func Render(size int) *image.RGBA {
	if size <= 0 {
		return nil
	}
	r := newRasteriser(size)
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	// Wings: the deep shade first, then the main blue over it — painting order
	// gives the overlap its depth without any blend mode.
	r.fillPolygon(bezierPath(lowerWing, 16), wingDeep)
	r.fillPolygon(mirrorX(bezierPath(lowerWing, 16)), wingDeep)
	r.fillPolygon(bezierPath(upperWing, 16), wingBlue)
	r.fillPolygon(mirrorX(bezierPath(upperWing, 16)), wingBlue)

	// White spots keep the mark legible at 16px.
	spotCenter, spotRad := wingSpot()
	r.fillCircle(spotCenter, spotRad, white)
	r.fillCircle(pt{x: 100 - spotCenter.x, y: spotCenter.y}, spotRad, white)

	// Body: dark slender lens.
	r.fillPolygon(bezierPath(bodyPath, 16), bodyDark)

	// Antennae: two thin curves.
	for _, left := range []bool{true, false} {
		r.strokePolyline(bezierPath(antenna(left), 12), antennaWidth, bodyDark)
	}

	return r.img
}
