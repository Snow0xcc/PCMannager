//go:build windows

package screenshot

import (
	"image"
	"math"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// annotKind identifies the shape an annotation draws.
type annotKind int

const (
	annotRect annotKind = iota
	annotEllipse
	annotArrow
	annotPen
)

// annot is one user-drawn shape. Shapes are stored in SCREEN coordinates (the
// editor window spans the captured display, so window and image coordinates
// coincide) and replayed on every paint, which is what makes undo trivial and
// keeps the original capture untouched.
type annot struct {
	Kind annotKind
	// From/To are the drag endpoints. For annotPen only From is used and Points
	// carries the freehand path.
	From, To winui.Rect
	// Points is the freehand path for annotPen.
	Points []winui.POINT
	// Color and Width are captured when the shape is finished, so later changes
	// to the toolbar do not retroactively restyle existing annotations.
	Color uint32
	Width int32
}

// Annotation toolbar geometry.
const (
	annotBarH      = 40
	annotBarPad    = 8
	annotSwatchW   = 26
	annotSwatchGap = 6
)

// Default drawing colours, as COLORREF (0x00BBGGRR). Red first, matching the
// conventional default in annotation tools.
var annotPalette = []uint32{
	winui.RGB(255, 59, 48),   // red
	winui.RGB(255, 204, 0),   // amber
	winui.RGB(52, 199, 89),   // green
	winui.RGB(0, 122, 255),   // blue
	winui.RGB(255, 255, 255), // white
	winui.RGB(0, 0, 0),       // black
}

// Annotation stroke widths offered by the toolbar.
var annotWidths = []int32{2, 4, 7}

// newPenStroke builds a one-point freehand path.
func newPenStroke(x, y int32) []winui.POINT {
	return []winui.POINT{{X: x, Y: y}}
}

// appendPenPoint extends a freehand path, noting that a new point is only worth
// keeping when it actually moved: dragging in place would otherwise grow the
// slice without changing the drawing.
func appendPenPoint(pts []winui.POINT, x, y int32) []winui.POINT {
	if n := len(pts); n > 0 {
		last := pts[n-1]
		if last.X == x && last.Y == y {
			return pts
		}
	}
	return append(pts, winui.POINT{X: x, Y: y})
}

// translated returns the annotation shifted by (dx,dy).
//
// Exporting uses this to move shapes from screen space into the cropped image's
// space: the crop's top-left corresponds to (r.Min - bounds.Min) in window
// coordinates, so that offset is subtracted from every stored point.
func (a annot) translated(dx, dy int32) annot {
	a.From = winui.Rect{Left: a.From.Left + dx, Top: a.From.Top + dy, Right: a.From.Right + dx, Bottom: a.From.Bottom + dy}
	a.To = winui.Rect{Left: a.To.Left + dx, Top: a.To.Top + dy, Right: a.To.Right + dx, Bottom: a.To.Bottom + dy}
	if len(a.Points) > 0 {
		pts := make([]winui.POINT, len(a.Points))
		for i, p := range a.Points {
			pts[i] = winui.POINT{X: p.X + dx, Y: p.Y + dy}
		}
		a.Points = pts
	}
	return a
}

// draw renders one annotation onto the canvas.
func (a annot) draw(c *winui.Canvas) {
	switch a.Kind {
	case annotRect:
		c.StrokeRect(a.From, a.Color, a.Width)
	case annotEllipse:
		c.StrokeEllipse(a.From, a.Color, a.Width)
	case annotArrow:
		a.drawArrow(c)
	case annotPen:
		c.StrokePolyline(a.Points, a.Color, a.Width)
	}
}

// drawArrow renders From -> To as a line with a V-shaped head at the tip.
//
// The head size scales with the stroke width so a thin arrow does not get a
// disproportionately large head.
func (a annot) drawArrow(c *winui.Canvas) {
	x1, y1 := a.From.Left, a.From.Top
	x2, y2 := a.To.Left, a.To.Top
	c.Line(x1, y1, x2, y2, a.Color, a.Width)

	dx := float64(x2 - x1)
	dy := float64(y2 - y1)
	length := math.Hypot(dx, dy)
	if length < 1 {
		return
	}
	head := float64(a.Width)*4 + 6
	if head > length {
		head = length
	}
	// Unit vector along the line, rotated +/- 30 degrees for the two barbs.
	const spread = 30 * math.Pi / 180
	for _, sign := range []float64{1, -1} {
		bx, by := arrowHeadBarb(x1, y1, x2, y2, spread, head, sign)
		c.Line(x2, y2, bx, by, a.Color, a.Width)
	}
}

// replay draws every annotation in order onto the canvas.
func replay(c *winui.Canvas, annots []annot) {
	for _, a := range annots {
		a.draw(c)
	}
}

// normalizePointRect orders the drag endpoints into a rectangle, so a shape
// dragged up-left is stored the same way as one dragged down-right.
func normalizePointRect(x1, y1, x2, y2 int32) winui.Rect {
	r := winui.Rect{Left: x1, Top: y1, Right: x2, Bottom: y2}
	if r.Left > r.Right {
		r.Left, r.Right = r.Right, r.Left
	}
	if r.Top > r.Bottom {
		r.Top, r.Bottom = r.Bottom, r.Top
	}
	return r
}

// lengthAtLeast reports whether the segment (x1,y1)→(x2,y2) is at least min
// pixels long in EITHER axis (Chebyshev distance).
//
// 箭头用 Chebyshev 距离而不是外接矩形的宽/高：一笔几乎纯垂直的箭头宽为 0，
// 矩形判定会把它当“没画”丢掉；而两点距离才是用户意图的正确表达。
func lengthAtLeast(x1, y1, x2, y2, min int32) bool {
	dx, dy := x1-x2, y1-y2
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx >= min || dy >= min
}

// arrowHeadBarb 计算箭头头部一根倒刺的端点。
//
// 纯函数抽出是为了可测试：它编码了“任意角度箭头”的全部数学——
// 沿线段方向的单位向量 (ux,uy) 旋转 ±spread 后，从箭尖往回推 head 长度。
// 修复前 From 被归一化到外接矩形左上角，导致从右下往左上画时方向反转、
// 只剩横竖方向碰巧“看起来对”。
func arrowHeadBarb(x1, y1, x2, y2 int32, spread, head float64, sign float64) (int32, int32) {
	dx := float64(x2 - x1)
	dy := float64(y2 - y1)
	length := math.Hypot(dx, dy)
	if length < 1 {
		return x2, y2
	}
	ux, uy := dx/length, dy/length
	ang := sign * spread
	bx := x2 - int32(head*(ux*math.Cos(ang)-uy*math.Sin(ang)))
	by := y2 - int32(head*(ux*math.Sin(ang)+uy*math.Cos(ang)))
	return bx, by
}

// cloneImage returns a copy of img, so annotations never mutate the capture the
// module may still hold a reference to.
func cloneImage(img *image.RGBA) *image.RGBA {
	if img == nil {
		return nil
	}
	out := image.NewRGBA(img.Bounds())
	copy(out.Pix, img.Pix)
	return out
}
