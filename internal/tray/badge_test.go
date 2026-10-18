package tray

import (
	"image"
	"image/color"
	"testing"

	"github.com/snow0xcc/pcmannager/internal/logo"
)

// badgeBase 造一张已知内容的底图：左下角蓝色、其余透明，用于断言
// "底图保留、角标叠加"互不侵犯。
func badgeBase(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	blue := color.RGBA{R: 0x4f, G: 0x8c, B: 0xff, A: 0xff}
	for y := h * 3 / 4; y < h; y++ {
		for x := 0; x < w/4; x++ {
			img.SetRGBA(x, y, blue)
		}
	}
	return img
}

// countRed 统计接近角标红的像素数。
func countRed(img *image.RGBA) int {
	n := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			c := img.RGBAAt(x, y)
			if c.A > 200 && c.R > 0xD0 && c.G < 0x80 && c.B < 0x80 {
				n++
			}
		}
	}
	return n
}

// countWhite 统计接近白色的像素数（角标数字）。
func countWhite(img *image.RGBA) int {
	n := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			c := img.RGBAAt(x, y)
			if c.A > 200 && c.R > 0xE0 && c.G > 0xE0 && c.B > 0xE0 {
				n++
			}
		}
	}
	return n
}

// TestBadgeOverlayDrawsBubbleAndDigits 守护角标的两个核心视觉要素：
// 右上角出现红色气泡、气泡里有白色数字。两者缺一，角标就是坏的。
func TestBadgeOverlayDrawsBubbleAndDigits(t *testing.T) {
	base := badgeBase(64, 64)
	got := BadgeOverlay(base, "7")
	if got == base {
		t.Fatal("应返回新图像而不是改写底图")
	}
	if n := countRed(got); n == 0 {
		t.Fatal("没有红色气泡像素")
	}
	if n := countWhite(got); n == 0 {
		t.Fatal("气泡里没有白色数字像素")
	}
	// 底图左下角的蓝色必须原样保留（合成不得侵蚀品牌底图）。
	c := got.RGBAAt(3, 60)
	if c.A != 0xff || c.B < c.R {
		t.Fatalf("底图像素被破坏: %+v", c)
	}
	// 底图本身不能被改写。
	if n := countRed(base); n != 0 {
		t.Fatal("底图被修改了")
	}
}

// TestBadgeOverlayEmptyKeepsBase 守护"空角标 = 原图标"：清除角标后不能残留
// 气泡，也不能返回 nil。
func TestBadgeOverlayEmptyKeepsBase(t *testing.T) {
	base := logo.Render(36)
	for _, text := range []string{"", "  "} {
		got := BadgeOverlay(base, text)
		if got == nil {
			t.Fatalf("text=%q 返回 nil", text)
		}
		if n := countRed(got); n != 0 {
			t.Fatalf("text=%q 不应有气泡: %d 个红像素", text, n)
		}
		if got.Bounds() != base.Bounds() {
			t.Fatalf("尺寸变了: %v -> %v", base.Bounds(), got.Bounds())
		}
	}
}

// TestBadgeOverlayUnknownCharsSkipped 守护未知字符被跳过而不是画成乱码：
// 角标内容来自状态数据，随时可能出现奇怪字符。
func TestBadgeOverlayUnknownCharsSkipped(t *testing.T) {
	got := BadgeOverlay(badgeBase(64, 64), "aπ✨7")
	if countWhite(got) == 0 {
		t.Fatal("合法字符 '7' 没有被绘制")
	}
}

// TestBadgeGlyphRunesClampsLongText 守护超长输入的截断策略：只留前两个字符
// 再补 '+'，气泡宽度是按三个字形设计的。
func TestBadgeGlyphRunesClampsLongText(t *testing.T) {
	got := badgeGlyphRunes("1234")
	want := []rune("12+")
	if string(got) != string(want) {
		t.Fatalf("badgeGlyphRunes = %q, 期望 %q", got, want)
	}
}

// TestBadgeOverlayHandlesMultiDigit 守护多位数与 '+'：宽度按字符数拉宽，
// 每个字形都要落在气泡内（至少有白色像素）。
func TestBadgeOverlayHandlesMultiDigit(t *testing.T) {
	for _, text := range []string{"12", "99+", "42"} {
		got := BadgeOverlay(badgeBase(64, 64), text)
		if countRed(got) == 0 || countWhite(got) == 0 {
			t.Fatalf("text=%q 气泡或数字缺失", text)
		}
	}
}

// TestBadgeOverlayNilBase 守护 nil 底图不 panic（托盘未渲染底图时的防御）。
func TestBadgeOverlayNilBase(t *testing.T) {
	if got := BadgeOverlay(nil, "7"); got != nil {
		t.Fatal("nil 底图应返回 nil")
	}
}
