//go:build windows

package launcher

import (
	"math"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// 菜单图标：右键菜单各项的 16px 矢量 glyph（GDI 图元，非真 SVG——项目零 cgo
// 无 SVG 解析器，用与磁贴图标同一套图元语言）。绘制在 winui 菜单 owner-draw
// 的图标方框内，textColor 跟随菜单文字色（选中态自动切高亮色）。

// menuIconPin 图钉（固定到前方）。
func menuIconPin(c *winui.Canvas, box winui.Rect, color uint32) {
	cx := (box.Left + box.Right) / 2
	cy := (box.Top + box.Bottom) / 2
	rad := box.Width() / 3
	fillCircle(c, cx, cy-rad/3, rad, color)
	c.Line(cx, cy, cx, box.Bottom, color, 1)
	c.Line(cx-rad/2, box.Bottom, cx+rad/2, box.Bottom, color, 1)
}

// menuIconEdit 铅笔（编辑关键字）。
func menuIconEdit(c *winui.Canvas, box winui.Rect, color uint32) {
	c.StrokePolyline([]winui.POINT{
		{X: box.Left + box.Width()/5, Y: box.Bottom - box.Height()/5},
		{X: box.Right - box.Width()/5, Y: box.Top + box.Height()/5},
		{X: box.Right - box.Width()/3, Y: box.Top + box.Height()/8},
		{X: box.Left + box.Width()/8, Y: box.Bottom - box.Height()/8},
		{X: box.Left + box.Width()/5, Y: box.Bottom - box.Height()/5},
	}, color, 1)
}

// menuIconCopy 两张叠纸（复制路径）。
func menuIconCopy(c *winui.Canvas, box winui.Rect, color uint32) {
	const sw = 1
	// 前页。
	c.StrokeRect(winui.Rect{
		Left: box.Left + box.Width()/5, Top: box.Top,
		Right: box.Right, Bottom: box.Bottom - box.Height()/5,
	}, color, sw)
	// 后页（左上偏移）。
	c.StrokePolyline([]winui.POINT{
		{X: box.Left + box.Width()/5, Y: box.Top + box.Height()/5},
		{X: box.Left, Y: box.Top + box.Height()/5},
		{X: box.Left, Y: box.Bottom},
		{X: box.Right - box.Width()/5, Y: box.Bottom},
		{X: box.Right - box.Width()/5, Y: box.Bottom - box.Height()/5},
	}, color, sw)
}

// menuIconFolderOpen 打开的文件夹（打开文件位置）。
func menuIconFolderOpen(c *winui.Canvas, box winui.Rect, color uint32) {
	c.StrokePolyline([]winui.POINT{
		{X: box.Left, Y: box.Bottom},
		{X: box.Left, Y: box.Top + box.Height()/4},
		{X: box.Left + box.Width()/4, Y: box.Top + box.Height()/4},
		{X: box.Left + box.Width()/3, Y: box.Top + box.Height()/2},
		{X: box.Right, Y: box.Top + box.Height()/2},
		{X: box.Right - box.Width()/6, Y: box.Bottom},
		{X: box.Left, Y: box.Bottom},
	}, color, 1)
}

// menuIconShield 盾牌（以管理员身份运行）。
func menuIconShield(c *winui.Canvas, box winui.Rect, color uint32) {
	cx := (box.Left + box.Right) / 2
	c.StrokePolyline([]winui.POINT{
		{X: cx, Y: box.Top},
		{X: box.Right - box.Width()/8, Y: box.Top + box.Height()/5},
		{X: box.Right - box.Width()/8, Y: box.Top + box.Height()/2},
		{X: cx, Y: box.Bottom},
		{X: box.Left + box.Width()/8, Y: box.Top + box.Height()/2},
		{X: box.Left + box.Width()/8, Y: box.Top + box.Height()/5},
		{X: cx, Y: box.Top},
	}, color, 1)
	c.Line(cx, box.Top+box.Height()/5, cx, box.Bottom-box.Height()/5, color, 1)
}

// menuIconTerminal 终端窗口（在终端打开此处）。
func menuIconTerminal(c *winui.Canvas, box winui.Rect, color uint32) {
	c.StrokeRect(box, color, 1)
	// 提示符 ">_"。
	c.StrokePolyline([]winui.POINT{
		{X: box.Left + box.Width()/5, Y: box.Top + box.Height()/3},
		{X: box.Left + box.Width()/2, Y: box.Top + box.Height()/2},
		{X: box.Left + box.Width()/5, Y: box.Top + box.Height()*2/3},
	}, color, 1)
	c.Line(box.Left+box.Width()/2+box.Width()/8, box.Top+box.Height()*2/3,
		box.Right-box.Width()/5, box.Top+box.Height()*2/3, color, 1)
}

// menuIconTrash 垃圾桶（清空排行榜/移出超级面板）。
func menuIconTrash(c *winui.Canvas, box winui.Rect, color uint32) {
	cx := (box.Left + box.Right) / 2
	c.StrokeRect(winui.Rect{
		Left: box.Left + box.Width()/4, Top: box.Top + box.Height()/4,
		Right: box.Right - box.Width()/4, Bottom: box.Bottom,
	}, color, 1)
	c.Line(box.Left+box.Width()/5, box.Top+box.Height()/4, box.Right-box.Width()/5, box.Top+box.Height()/4, color, 1)
	c.Line(cx, box.Top, cx, box.Top+box.Height()/4, color, 1)
}

// menuIconSuper 星标（固定到超级面板）。
func menuIconSuper(c *winui.Canvas, box winui.Rect, color uint32) {
	cx := (box.Left + box.Right) / 2
	cy := (box.Top + box.Bottom) / 2
	r := float64(box.Width() / 2)
	pts := make([]winui.POINT, 0, 10)
	for i := 0; i < 10; i++ {
		ang := -math.Pi/2 + float64(i)*math.Pi/5
		rad := r
		if i%2 == 1 {
			rad = r * 0.45
		}
		pts = append(pts, winui.POINT{
			X: cx + int32(rad*math.Cos(ang)),
			Y: cy + int32(rad*math.Sin(ang)),
		})
	}
	c.StrokePolyline(pts, color, 1)
}
