//go:build windows

package screenshot

import (
	"math"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// Toolbar glyphs are composed from GDI vector primitives (lines, rectangles,
// ellipses, polygons) rather than loaded from an icon font, a bitmap or an SVG.
//
// The trade-off, stated explicitly: internal/winui has no SVG parser, and adding
// a renderer would pull a sizeable dependency (or cgo) into a build that is
// deliberately dependency-light and strictly CGO_ENABLED=0 — a non-starter for
// this project. A dozen primitives per glyph is enough for every toolbar button,
// scales with the button box, costs no icon assets beside the exe, and keeps the
// front-end rule ("no emoji, use icon resources") satisfiable on the native side
// without shipping any resource files.
//
// Every glyph is drawn inside a centred square derived from the button
// rectangle, so the toolbar stays aligned whatever size the caller passes.

// iconArcSegments is the number of straight segments used to approximate the one
// arc glyph (undo). Twelve is invisible as faceting at icon size and cheap.
const iconArcSegments = 12

// iconBox returns the centred square that a toolbar glyph is inscribed in.
//
// The glyph occupies about half of r's short side: large enough to read, small
// enough that it does not collide with the button's own border. When r is too
// small for that (or degenerate), the second return value is false and the
// caller must draw nothing rather than risk painting outside r.
func iconBox(r winui.Rect) (winui.Rect, bool) {
	short := r.Width()
	if r.Height() < short {
		short = r.Height()
	}
	if short <= 4 {
		return winui.Rect{}, false
	}

	side := short / 2
	if side < 4 {
		// Tiny buttons: half would vanish into a dot, so use nearly the whole
		// short side while still leaving a 1px margin per edge.
		side = short - 2
	}
	if max := short - 2; side > max {
		side = max
	}
	if side <= 0 {
		return winui.Rect{}, false
	}

	cx := r.Left + r.Width()/2
	cy := r.Top + r.Height()/2
	half := side / 2
	return winui.Rect{Left: cx - half, Top: cy - half, Right: cx + half, Bottom: cy + half}, true
}

// iconStroke picks a pen width for a glyph: roughly a sixth of the box, clamped
// so a tiny icon still gets a hairline and a large one does not turn into a
// blob.
func iconStroke(box winui.Rect) int32 {
	w := box.Width() / 6
	if w < 1 {
		w = 1
	}
	if w > 3 {
		w = 3
	}
	return w
}

// fillCircle paints a filled disc of radius rad centred at (cx, cy).
//
// Win32's Ellipse fills only with a selected brush, which this wrapper does not
// expose, so the disc is approximated by a polygon — indistinguishable from a
// circle at glyph size.
func fillCircle(c *winui.Canvas, cx, cy, rad int32, color uint32) {
	if rad < 1 {
		rad = 1
	}
	const segments = 16
	pts := make([]winui.POINT, 0, segments)
	for i := 0; i < segments; i++ {
		a := 2 * math.Pi * float64(i) / segments
		pts = append(pts, winui.POINT{
			X: cx + int32(float64(rad)*math.Cos(a)),
			Y: cy + int32(float64(rad)*math.Sin(a)),
		})
	}
	c.FillPolygon(pts, color)
}

// drawToolbarIcon renders the vector glyph for a toolbar button id, centred in r.
//
// It draws the glyph only: the button plate, its background and its label are the
// caller's (drawToolbarButton) responsibility, so the icon can be overlaid on any fill
// colour without knowing it.
func drawToolbarIcon(c *winui.Canvas, r winui.Rect, id int, fg uint32) {
	if c == nil {
		return
	}
	box, ok := iconBox(r)
	if !ok {
		// Degenerate button: no room for a legible glyph.
		return
	}
	stroke := iconStroke(box)
	// Keep the whole pen (not just its centre line) inside the box.
	b := box.Inset(stroke)
	if b.Width() <= 0 || b.Height() <= 0 {
		b = box
	}

	switch id {
	case edBtnConfirm:
		// Check mark: down-right, then up-right.
		c.StrokePolyline([]winui.POINT{
			{X: b.Left, Y: b.Top + b.Height()/2},
			{X: b.Left + b.Width()/3, Y: b.Bottom},
			{X: b.Right, Y: b.Top},
		}, fg, stroke)

	case edBtnCopy:
		// Two offset sheets. The back sheet is an open polyline that stops short
		// of the front one: at icon size a full second rectangle would cross the
		// front sheet's edges and read as a grid instead of as two objects.
		off := b.Width() / 3
		c.StrokePolyline([]winui.POINT{
			{X: b.Left, Y: b.Bottom - off},
			{X: b.Left, Y: b.Top},
			{X: b.Right - off, Y: b.Top},
			{X: b.Right - off, Y: b.Top + off},
		}, fg, stroke)
		c.StrokeRect(winui.Rect{
			Left: b.Left + off, Top: b.Top + off, Right: b.Right, Bottom: b.Bottom,
		}, fg, stroke)

	case edBtnCancel:
		// Cross.
		c.Line(b.Left, b.Top, b.Right, b.Bottom, fg, stroke)
		c.Line(b.Right, b.Top, b.Left, b.Bottom, fg, stroke)

	case edBtnUndo:
		// An arc sweeping over the top from left to right with an arrowhead at
		// the left tip: the conventional "undo" curl.
		cx := (b.Left + b.Right) / 2
		cy := (b.Top + b.Bottom) / 2
		rx := float64(b.Width()) / 2
		ry := float64(b.Height()) / 2
		pts := make([]winui.POINT, 0, iconArcSegments+1)
		for i := 0; i <= iconArcSegments; i++ {
			// y grows downwards in GDI, so the negative sine puts the arc above cy.
			t := math.Pi * float64(i) / iconArcSegments
			pts = append(pts, winui.POINT{
				X: cx - int32(rx*math.Cos(t)),
				Y: cy - int32(ry*math.Sin(t)),
			})
		}
		c.StrokePolyline(pts, fg, stroke)

		tip := pts[0]
		hd := b.Height()/2 + stroke
		if hd < 4 {
			hd = 4
		}
		// Left-pointing barbs, so the head closes the open end of the arc.
		c.Line(tip.X, tip.Y, tip.X+hd, tip.Y-hd, fg, stroke)
		c.Line(tip.X, tip.Y, tip.X+hd, tip.Y+hd, fg, stroke)

	case edBtnRect:
		c.StrokeRect(b, fg, stroke)

	case edBtnEllipse:
		c.StrokeEllipse(b, fg, stroke)

	case edBtnArrow:
		// Diagonal shaft with a head at the tip.
		c.Line(b.Left, b.Bottom, b.Right, b.Top, fg, stroke)
		hd := b.Width()/3 + stroke
		c.Line(b.Right, b.Top, b.Right-hd, b.Top, fg, stroke)
		c.Line(b.Right, b.Top, b.Right, b.Top+hd, fg, stroke)

	case edBtnPen:
		// A thick diagonal body, a short cross-band (the ferrule) and a solid
		// triangular nib between the band and the paper.
		nx, ny := b.Left, b.Bottom
		c.Line(nx, ny, b.Right, b.Top, fg, stroke*2)

		fx := b.Left + b.Width()/3
		fy := b.Bottom - b.Height()/3
		half := b.Width() / 5
		if half < 3 {
			half = 3
		}
		// The body runs at 45 degrees, so its perpendicular is (+1,+1).
		a1 := winui.POINT{X: fx - half, Y: fy - half}
		a2 := winui.POINT{X: fx + half, Y: fy + half}
		c.StrokePolyline([]winui.POINT{a1, a2}, fg, stroke)
		c.FillPolygon([]winui.POINT{{X: nx, Y: ny}, a1, a2}, fg)

	case edBtnRecord:
		// Outer ring plus solid centre: a record button.
		c.StrokeEllipse(b, fg, stroke)
		fillCircle(c, (b.Left+b.Right)/2, (b.Top+b.Bottom)/2, b.Width()/3, fg)

	case edBtnScrollStart:
		// Vertical double-headed arrow: scrolling both ways.
		cx := (b.Left + b.Right) / 2
		hd := b.Width() / 3
		if hd < 3 {
			hd = 3
		}
		if hd > b.Height()/2 {
			hd = b.Height() / 2
		}
		c.Line(cx, b.Top+hd, cx, b.Bottom-hd, fg, stroke)
		c.FillPolygon([]winui.POINT{
			{X: cx, Y: b.Top},
			{X: cx - hd, Y: b.Top + hd},
			{X: cx + hd, Y: b.Top + hd},
		}, fg)
		c.FillPolygon([]winui.POINT{
			{X: cx, Y: b.Bottom},
			{X: cx - hd, Y: b.Bottom - hd},
			{X: cx + hd, Y: b.Bottom - hd},
		}, fg)

	case edBtnScrollAuto:
		// A toggle switch with the knob thrown to the right, i.e. "on": the
		// auto-scroll state is a switch, not a one-shot action, and a second set
		// of arrows next to edBtnScrollStart would be ambiguous.
		cy := (b.Top + b.Bottom) / 2
		track := winui.Rect{
			Left: b.Left, Top: cy - b.Height()/4,
			Right: b.Right, Bottom: cy + b.Height()/4,
		}
		c.StrokeRect(track, fg, stroke)
		knobR := track.Height()
		if knobR < 2 {
			knobR = 2
		}
		fillCircle(c, track.Right-knobR, cy, knobR, fg)

	default:
		// Unknown id: a dot keeps the button from looking empty while never
		// claiming a meaning it does not have.
		fillCircle(c, (b.Left+b.Right)/2, (b.Top+b.Bottom)/2, b.Width()/4, fg)
	}
}

// drawDragHandle draws the six-dot grab handle (2 columns x 3 rows).
//
// The dot diameter is derived from r rather than fixed: the handle is used both
// in the toolbar and on the move bars of an annotation, which are sized from the
// annotation's stroke width.
func drawDragHandle(c *winui.Canvas, r winui.Rect, fg uint32) {
	if c == nil || r.Width() <= 4 || r.Height() <= 4 {
		return
	}

	// Three rows of dots plus two one-dot gaps is five dot diameters tall, and
	// two columns plus one gap is three across; take whichever axis is tighter.
	dot := (r.Height() - 4) / 5
	if maxW := (r.Width() - 4) / 3; dot > maxW {
		dot = maxW
	}
	if dot < 2 {
		dot = 2
	}
	rad := dot / 2
	if rad < 1 {
		rad = 1
	}

	const cols, rows = 2, 3
	totalW := int32(cols)*dot + int32(cols-1)*dot
	totalH := int32(rows)*dot + int32(rows-1)*dot
	left := r.Left + (r.Width()-totalW)/2
	top := r.Top + (r.Height()-totalH)/2

	for row := int32(0); row < rows; row++ {
		for col := int32(0); col < cols; col++ {
			cx := left + col*(dot*2) + rad
			cy := top + row*(dot*2) + rad
			fillCircle(c, cx, cy, rad, fg)
		}
	}
}
