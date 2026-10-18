package tray

import (
	xdraw "golang.org/x/image/draw"
	"image"
	"image/color"
	"image/draw"
	"math"
)

// 托盘角标：把一个小的红色气泡（如剪贴板历史条数）叠画到托盘图标右上角。
//
// 三平台共用这一份绘制逻辑（无 build tag）：macOS 把合成结果转成 NSImage，
// Linux 转成 IconPixmap，Windows 转成 HICON。刻意不复用字库/字体包——数字只有
// 十个，3x5 的手工点阵足够，且与 internal/logo 的"程序化绘制、不引依赖"一脉相承。

// badgeRed 是角标气泡的颜色（接近 iOS 约定红），带不透明 alpha。
var badgeRed = color.RGBA{R: 0xFF, G: 0x3B, B: 0x30, A: 0xFF}

// badgeGlyphs 是 3x5 点阵字库：0-9 与 '+'、'!'。
// 每个字形 5 行、每行 3 位，1 为着墨。
var badgeGlyphs = map[rune][5]string{
	'0': {"111", "101", "101", "101", "111"},
	'1': {"010", "110", "010", "010", "111"},
	'2': {"111", "001", "111", "100", "111"},
	'3': {"111", "001", "111", "001", "111"},
	'4': {"101", "101", "111", "001", "001"},
	'5': {"111", "100", "111", "001", "111"},
	'6': {"111", "100", "111", "101", "111"},
	'7': {"111", "001", "001", "001", "001"},
	'8': {"111", "101", "111", "101", "111"},
	'9': {"111", "101", "111", "001", "111"},
	'+': {"000", "010", "111", "010", "000"},
	'!': {"010", "010", "010", "000", "010"},
}

// badgeMaxGlyphs 限制角标字符数：托盘图标很小，三个字符是可辨识的上限。
const badgeMaxGlyphs = 3

// badgeGlyphRunes 把 text 过滤成可绘制的字形序列。
//
// 未收录的字符被跳过；超过上限时截短为"前两个字符 + '+'"（如 1234 → 12+），
// 保证任意输入都能落进气泡里。
func badgeGlyphRunes(text string) []rune {
	var out []rune
	for _, r := range text {
		if _, ok := badgeGlyphs[r]; ok {
			out = append(out, r)
		}
	}
	if len(out) > badgeMaxGlyphs {
		out = append(out[:badgeMaxGlyphs-1], '+')
	}
	return out
}

// BadgeOverlay 把 text 对应的角标画到 base 的右上角，返回**新的**图像
// （base 不被修改，调用方可以缓存它反复合成）。text 为空或没有可绘字符时
// 返回 base 的等价副本。
//
// 合成是逐像素的：气泡为不透明红色圆形（字符多时拉宽为胶囊形），数字为白色
// 点阵；base 的其它像素原样保留。为平滑起见先按 supersample 倍率放大绘制，
// 再用近似双线性缩回目标尺寸。
func BadgeOverlay(base *image.RGBA, text string) *image.RGBA {
	if base == nil {
		return nil
	}
	out := image.NewRGBA(base.Bounds())
	draw.Draw(out, out.Bounds(), base, image.Point{}, draw.Src)

	glyphs := badgeGlyphRunes(text)
	if len(glyphs) == 0 {
		return out
	}

	const supersample = 4
	b := base.Bounds()
	w, h := b.Dx(), b.Dy()
	canvas := image.NewRGBA(image.Rect(0, 0, w*supersample, h*supersample))

	// 气泡几何：直径约为图标高度的 0.5，多字符时按比例拉宽；右上角留 6% 边距。
	dia := float64(h*supersample) * 0.50
	charW := dia * 0.62
	bubW := dia*0.55 + charW*float64(len(glyphs)-1) + dia*0.06
	if len(glyphs) == 1 {
		bubW = dia
	}
	bubH := dia
	cx := float64(w*supersample) - bubW/2 - float64(w*supersample)*0.06
	cy := float64(h*supersample)*0.06 + bubH/2

	// 气泡：逐像素距离判定（圆或圆角胶囊），无需 path 库。
	x0 := int(cx - bubW/2 - 1)
	x1 := int(cx + bubW/2 + 1)
	y0 := int(cy - bubH/2 - 1)
	y1 := int(cy + bubH/2 + 1)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if x < 0 || y < 0 || x >= canvas.Bounds().Dx() || y >= canvas.Bounds().Dy() {
				continue
			}
			if !insideBubble(float64(x)+0.5, float64(y)+0.5, cx, cy, bubW, bubH) {
				continue
			}
			canvas.SetRGBA(x, y, badgeRed)
		}
	}

	// 字形：点阵按气泡高度缩放，整体在气泡内水平垂直居中。
	cell := bubH * 0.62 / 5 // 每个点阵格的边长
	gridW := 3*cell + cell*0.6*(float64(len(glyphs))-1)
	gridH := 5 * cell
	gx0 := cx - gridW/2
	gy0 := cy - gridH/2
	white := color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	for gi, g := range glyphs {
		rows := badgeGlyphs[g]
		ox := gx0 + cell*0.6*float64(gi)
		for r, row := range rows {
			for c, mark := range row {
				if mark != '1' {
					continue
				}
				px0 := int(math.Round(ox + float64(c)*cell))
				py0 := int(math.Round(gy0 + float64(r)*cell))
				px1 := int(math.Round(ox + float64(c+1)*cell))
				py1 := int(math.Round(gy0 + float64(r+1)*cell))
				for y := py0; y < py1; y++ {
					for x := px0; x < px1; x++ {
						if x < 0 || y < 0 || x >= canvas.Bounds().Dx() || y >= canvas.Bounds().Dy() {
							continue
						}
						canvas.SetRGBA(x, y, white)
					}
				}
			}
		}
	}

	// 缩回目标尺寸并以 Over 叠回底图：canvas 里只有气泡与数字（其余透明），
	// 若用 Src 会把整张画布盖回去、丢掉底图——首版实现犯过这个错，靠
	// TestBadgeOverlayDrawsBubbleAndDigits 的底图保留断言抓了出来。气泡与数字
	// 都不透明，透明边缘用近似双线性混合，Over 叠加是安全的。
	xdraw.ApproxBiLinear.Scale(out, out.Bounds(), canvas, canvas.Bounds(), draw.Over, nil)
	return out
}

// insideBubble 判定点是否在气泡内：单字符是圆，多字符是两端半圆的胶囊。
func insideBubble(px, py, cx, cy, w, h float64) bool {
	r := h / 2
	if w <= h {
		return math.Hypot(px-cx, py-cy) <= r
	}
	half := w/2 - r
	// 胶囊：中段是矩形，两端是圆。
	if px >= cx-half && px <= cx+half {
		return math.Abs(py-cy) <= r
	}
	edge := cx - half
	if px > cx {
		edge = cx + half
	}
	return math.Hypot(px-edge, py-cy) <= r
}
