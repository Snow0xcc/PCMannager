package logo

import (
	"fmt"
	"image/color"
	"strings"
)

// SVG 输出：把本包 `Render` 用的同一套几何与配色序列化成静态 SVG，供 README 页眉、
// Wiki 与 GitHub Pages 展示使用。
//
// 方向是**单向**的（Go 几何 → 文档 SVG）：程序自身不解析 SVG——托盘图标走
// `Render` 直接光栅化成 HICON（见 internal/tray 的 brandIconHandle），这里生成的
// 只是同一个几何在文档里的镜像。资源由 `scripts/gen-logo.sh` 生成，改品牌形状或
// 配色请改本包的几何常量后重新生成，不要手改 SVG。
//
// 坐标系沿用 `Render` 的 0..100 归一化空间：`MarkSVG` 直接把它作为 viewBox，
// `LogoSVG` 再把图标缩放到页眉尺寸并拼上字标。

// 页眉版式常量（`LogoSVG`）：图标 64px，与字标留 18 的间距；字标宽度由
// `textLength` 锁定，所以总宽固定为 292。
const (
	logoWidth    = 292
	logoHeight   = 64
	markSize     = 64
	markGap      = 18
	wordmarkSize = 34
	wordmarkLen  = 192
	// markShiftY 是实测微调量：蝴蝶的着墨重心（翅膀加机身）低于几何包围盒中心，
	// 不补这一项图标会明显比字标高。
	markShiftY = 0.96
)

// svgWhite 是翅斑的白，与 `Render` 里的局部 white 同值。
var svgWhite = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

// svgHex 把品牌色写成 SVG 的 #RRGGBB（大写）。
func svgHex(c color.RGBA) string {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

// svgNum 用 %g 写坐标：几何常量都是整数，因此不会出现多余的尾随零。
func svgNum(v float64) string { return fmt.Sprintf("%g", v) }

// svgPath 把一段或多段连续的三次贝塞尔写成 SVG path 的 d。
// closed 决定是否收笔：填充轮廓需要 Z，描边路径不能要。
func svgPath(segs [][4]pt, closed bool) string {
	if len(segs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("M" + svgNum(segs[0][0].x) + " " + svgNum(segs[0][0].y))
	for _, s := range segs {
		b.WriteString("C")
		for i, p := range s[1:] {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString(svgNum(p.x) + " " + svgNum(p.y))
		}
	}
	if closed {
		b.WriteString("Z")
	}
	return b.String()
}

// mirrorSegs 把贝塞尔控制点沿 x=50 折返，得到右半边（与 `mirrorX` 对采样点做的事
// 一样，只是作用在控制点上，SVG 才能画出一模一样的镜像曲线）。
func mirrorSegs(segs [][4]pt) [][4]pt {
	out := make([][4]pt, len(segs))
	for i, s := range segs {
		for j, p := range s {
			out[i][j] = pt{x: 100 - p.x, y: p.y}
		}
	}
	return out
}

// svgMark 输出品牌蝴蝶在 0..100 坐标系下的图形片段（不含 <svg> 外壳），
// 每行缩进 indent，便于直接嵌进 `LogoSVG` 的 <g> 里。
func svgMark(indent string) string {
	var b strings.Builder
	p := func(format string, args ...any) {
		fmt.Fprintf(&b, indent+format+"\n", args...)
	}
	// 绘制顺序与 `Render` 一致：下翅（深色）→ 上翅（主色）→ 翅斑 → 机身 → 触角。
	p(`<path fill="%s" d="%s"/>`, svgHex(wingDeep), svgPath(lowerWing, true))
	p(`<path fill="%s" d="%s"/>`, svgHex(wingDeep), svgPath(mirrorSegs(lowerWing), true))
	p(`<path fill="%s" d="%s"/>`, svgHex(wingBlue), svgPath(upperWing, true))
	p(`<path fill="%s" d="%s"/>`, svgHex(wingBlue), svgPath(mirrorSegs(upperWing), true))
	spot, rad := wingSpot()
	p(`<circle fill="%s" cx="%s" cy="%s" r="%s"/>`, svgHex(svgWhite), svgNum(spot.x), svgNum(spot.y), svgNum(rad))
	p(`<circle fill="%s" cx="%s" cy="%s" r="%s"/>`, svgHex(svgWhite), svgNum(100-spot.x), svgNum(spot.y), svgNum(rad))
	p(`<path fill="%s" d="%s"/>`, svgHex(bodyDark), svgPath(bodyPath, true))
	p(`<g fill="none" stroke="%s" stroke-width="%s" stroke-linecap="round" stroke-linejoin="round">`,
		svgHex(bodyDark), svgNum(antennaWidth))
	p(`  <path d="%s"/>`, svgPath(antenna(true), false))
	p(`  <path d="%s"/>`, svgPath(antenna(false), false))
	p(`</g>`)
	return b.String()
}

// svgGenerated 是生成文件的标记注释：提醒后来者不要手改（下次生成会被覆盖）。
func svgGenerated(indent string) string {
	return indent + "<!-- 由 scripts/gen-logo.sh 依据 internal/logo 的几何生成，请勿手工编辑。 -->\n"
}

// MarkSVG 返回仅含品牌蝴蝶的独立 SVG 文档（正方形，viewBox 0..100）。
func MarkSVG() string {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 100 100" role="img" aria-labelledby="pm-mark-title">` + "\n")
	b.WriteString(`  <title id="pm-mark-title">PCMannager</title>` + "\n")
	b.WriteString(svgGenerated("  "))
	b.WriteString(svgMark("  "))
	b.WriteString("</svg>\n")
	return b.String()
}

// LogoSVG 返回页眉用的完整 SVG：左侧品牌蝴蝶 + 右侧 PCMannager 字标。
//
// 字标用 <text> 而非路径：它是文档展示资源，跟随系统字体；宽度由 textLength
// 锁定，字体回退时也不会溢出画布。明暗自适应靠 SVG 内部的 media query——
// README / Wiki / Pages 的背景色并不一致。
func LogoSVG() string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="pm-logo-title">`+"\n",
		logoWidth, logoHeight, logoWidth, logoHeight)
	b.WriteString(`  <title id="pm-logo-title">PCMannager</title>` + "\n")
	b.WriteString(svgGenerated("  "))
	b.WriteString("  <defs>\n")
	b.WriteString("    <style>\n")
	b.WriteString("      /* 字标跟随宿主明暗：README / Wiki / Pages 背景不同，靠这里自适应。 */\n")
	b.WriteString("      .pm-word {\n")
	b.WriteString(`        font-family: "Segoe UI", -apple-system, BlinkMacSystemFont, "Helvetica Neue", Arial, sans-serif;` + "\n")
	fmt.Fprintf(&b, "        font-size: %dpx;\n", wordmarkSize)
	b.WriteString("        font-weight: 700;\n")
	b.WriteString("        fill: #1f2328;\n")
	b.WriteString("      }\n")
	b.WriteString("      @media (prefers-color-scheme: dark) {\n")
	b.WriteString("        .pm-word { fill: #e6edf3; }\n")
	b.WriteString("      }\n")
	b.WriteString("    </style>\n")
	b.WriteString("  </defs>\n")
	// 图标从 0..100 空间缩放到 markSize，并按实测重心下移 markShiftY。
	fmt.Fprintf(&b, `  <g transform="translate(0,%s) scale(%s)">`+"\n",
		svgNum(markShiftY), svgNum(float64(markSize)/100.0))
	b.WriteString(svgMark("    "))
	b.WriteString("  </g>\n")
	fmt.Fprintf(&b, `  <text class="pm-word" x="%d" y="44" textLength="%d" lengthAdjust="spacing">PCMannager</text>`+"\n",
		markSize+markGap, wordmarkLen)
	b.WriteString("</svg>\n")
	return b.String()
}
