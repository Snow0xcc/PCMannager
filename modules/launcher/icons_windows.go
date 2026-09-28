//go:build windows

package launcher

import (
	"math"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// 面板图标用 GDI 矢量图元绘制（与截图工具栏同一理由：项目零 cgo + 无 SVG
// 解析器）。每个 glyph 画在 tile 的 icon 区域内，用当前颜色。
//
// iconTileRect 返回 tile 内图标占用的方框（居中，约占 tile 高的一半）。

// drawTileIcon 按 glyph 键画一个候选磁贴图标。
func drawTileIcon(c *winui.Canvas, box winui.Rect, glyph string, color uint32) {
	if c == nil || box.Width() <= 8 || box.Height() <= 8 {
		return
	}
	// 内缩一个笔宽，保证线描不越界。
	b := box.Inset(2)
	if b.Width() <= 0 || b.Height() <= 0 {
		return
	}
	sw := iconStrokeWidth(box)
	cx := (b.Left + b.Right) / 2
	cy := (b.Top + b.Bottom) / 2

	switch glyph {
	case "camera":
		// 相机：机身矩形 + 顶部取景器 + 镜头圆。
		c.StrokeRect(winui.Rect{Left: b.Left, Top: b.Top + b.Height()/4, Right: b.Right, Bottom: b.Bottom - b.Height()/8}, color, sw)
		c.StrokeRect(winui.Rect{Left: cx - b.Width()/6, Top: b.Top, Right: cx + b.Width()/6, Bottom: b.Top + b.Height()/4}, color, sw)
		fillCircle(c, cx, cy+b.Height()/16, b.Width()/5, color)

	case "clipboard":
		// 剪贴板：夹子 + 板身。
		c.StrokeRect(winui.Rect{Left: b.Left + b.Width()/5, Top: b.Top, Right: b.Right - b.Width()/5, Bottom: b.Bottom}, color, sw)
		c.Line(b.Left+b.Width()/3, b.Top, b.Left+b.Width()/3, b.Top+b.Height()/5, color, sw)
		c.Line(b.Right-b.Width()/3, b.Top, b.Right-b.Width()/3, b.Top+b.Height()/5, color, sw)
		c.StrokeRect(winui.Rect{Left: b.Left, Top: b.Top, Right: b.Left + b.Width()/5, Bottom: b.Top + b.Height()/5}, color, sw)
		c.StrokeRect(winui.Rect{Left: b.Right - b.Width()/5, Top: b.Top, Right: b.Right, Bottom: b.Top + b.Height()/5}, color, sw)

	case "monitor":
		c.StrokeRect(b, color, sw)
		c.Line(b.Left, b.Bottom, b.Right, b.Bottom, color, sw)
		c.Line(cx, b.Bottom, cx, b.Bottom+b.Height()/5, color, sw)
		c.Line(cx-b.Width()/4, b.Bottom+b.Height()/5, cx+b.Width()/4, b.Bottom+b.Height()/5, color, sw)

	case "wrench":
		// 扳手：圆环开口 + 斜柄。
		arcPoints := func() []winui.POINT {
			var pts []winui.POINT
			for i := 0; i <= 16; i++ {
				a := math.Pi * 0.9 * (float64(i)/16 - 0.5) // 从 -0.45π 到 +0.45π 的开口环
				pts = append(pts, winui.POINT{
					X: cx - int32(float64(b.Width()/4)*math.Cos(a)),
					Y: cy - int32(float64(b.Height()/4)*math.Sin(a)),
				})
			}
			return pts
		}
		c.StrokePolyline(arcPoints(), color, sw)
		c.Line(cx+b.Width()/6, cy+b.Height()/6, b.Right, b.Bottom, color, sw)

	case "download":
		c.Line(cx, b.Top, cx, b.Bottom-b.Height()/4, color, sw)
		c.StrokePolyline([]winui.POINT{
			{X: cx - b.Width()/3, Y: b.Bottom - b.Height()/3},
			{X: cx, Y: b.Bottom},
			{X: cx + b.Width()/3, Y: b.Bottom - b.Height()/3},
		}, color, sw)

	case "search":
		fillCircle(c, cx-b.Width()/6, cy-b.Height()/6, b.Width()/4, color)
		c.Line(cx+b.Width()/8, cy+b.Height()/8, b.Right, b.Bottom, color, sw)

	case "history":
		fillCircle(c, cx, cy, b.Width()/4, color)
		c.Line(cx, cy, cx, b.Top, color, sw)
		c.Line(cx, cy, cx+b.Width()/6, cy+b.Height()/6, color, sw)

	case "web":
		fillCircle(c, cx, cy, b.Width()/4, color)
		c.StrokeEllipse(winui.Rect{Left: cx - b.Width()/3, Top: cy - b.Height()/6, Right: cx + b.Width()/3, Bottom: cy + b.Height()/6}, color, sw)
		c.Line(cx-b.Width()/3, cy, cx+b.Width()/3, cy, color, sw)

	case "bolt":
		c.FillPolygon([]winui.POINT{
			{X: cx + b.Width()/8, Y: b.Top},
			{X: cx - b.Width()/4, Y: cy},
			{X: cx, Y: cy},
			{X: cx - b.Width()/8, Y: b.Bottom},
			{X: cx + b.Width()/4, Y: cy},
			{X: cx, Y: cy},
		}, color)

	case "module":
		fallthrough
	default:
		// 通用九宫格：模块的兜底 glyph。
		third := b.Width() / 3
		c.StrokeRect(winui.Rect{Left: b.Left, Top: b.Top, Right: b.Left + third, Bottom: b.Top + third}, color, sw)
		c.StrokeRect(winui.Rect{Left: b.Right - third, Top: b.Top, Right: b.Right, Bottom: b.Top + third}, color, sw)
		c.StrokeRect(winui.Rect{Left: b.Left, Top: b.Bottom - third, Right: b.Left + third, Bottom: b.Bottom}, color, sw)
		c.StrokeRect(winui.Rect{Left: b.Right - third, Top: b.Bottom - third, Right: b.Right, Bottom: b.Bottom}, color, sw)
	}
}

// iconStrokeWidth 由 tile 尺寸推出线宽（最大 3px）。
func iconStrokeWidth(box winui.Rect) int32 {
	w := box.Width() / 10
	if w < 1 {
		w = 1
	}
	if w > 3 {
		w = 3
	}
	return w
}

// fillCircle 画实心圆（用多边形近似，与截图图标实现一致）。
func fillCircle(c *winui.Canvas, cx, cy, rad int32, color uint32) {
	if rad < 1 {
		rad = 1
	}
	const seg = 16
	pts := make([]winui.POINT, 0, seg)
	for i := 0; i < seg; i++ {
		a := 2 * math.Pi * float64(i) / seg
		pts = append(pts, winui.POINT{
			X: cx + int32(float64(rad)*math.Cos(a)),
			Y: cy + int32(float64(rad)*math.Sin(a)),
		})
	}
	c.FillPolygon(pts, color)
}
