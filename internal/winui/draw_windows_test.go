//go:build windows

package winui

import (
	"image"
	"testing"
)

// procGetPixelRead is the readback used by these tests; GetPixel is not part of
// the production surface (nothing paints single pixels back).
var procGetPixelRead = gdi32.NewProc("GetPixel")

// pixelReader builds a memory DC of the given size with a cleanup function and
// a per-pixel readback helper.
func pixelReader(t *testing.T, w, h int32) (uintptr, func(), func(x, y int32) uint32) {
	t.Helper()

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		t.Skip("没有可用的屏幕 DC")
	}
	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		procReleaseDC.Call(0, screenDC)
		t.Skip("CreateCompatibleDC 失败")
	}
	hbm, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(w), uintptr(h))
	if hbm == 0 {
		procDeleteDC.Call(memDC)
		procReleaseDC.Call(0, screenDC)
		t.Skip("CreateCompatibleBitmap 失败")
	}
	old, _, _ := procSelectObject.Call(memDC, hbm)

	cleanup := func() {
		procSelectObject.Call(memDC, old)
		procDeleteObject.Call(hbm)
		procDeleteDC.Call(memDC)
		procReleaseDC.Call(0, screenDC)
	}
	read := func(x, y int32) uint32 {
		r, _, _ := procGetPixelRead.Call(memDC, uintptr(x), uintptr(y))
		return uint32(r)
	}
	return memDC, cleanup, read
}

// solidImage builds a w*h RGBA image filled with one colour.
func solidImage(w, h int, r, g, b byte) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+0] = r
		img.Pix[i+1] = g
		img.Pix[i+2] = b
		img.Pix[i+3] = 255
	}
	return img
}

// TestCanvasImageTransfersColor is the regression for the screenshot editor
// rendering a black surface: it paints a solid colour into a memory DC and
// reads it back, so a silent transfer failure shows up as the pre-fill colour.
func TestCanvasImageTransfersColor(t *testing.T) {
	memDC, cleanup, read := pixelReader(t, 32, 32)
	defer cleanup()

	c := &Canvas{hdc: memDC}
	magenta := RGB(255, 0, 255)
	c.Fill(Rect{Right: 32, Bottom: 32}, magenta)
	c.Image(Rect{Right: 32, Bottom: 32}, solidImage(32, 32, 0, 255, 0))

	if got, want := read(16, 16), RGB(0, 255, 0); got != want {
		t.Fatalf("Canvas.Image 未写入图像: 取到 0x%06X, 期望绿色 0x%06X (0x%06X 表示完全没绘制)",
			got, want, magenta)
	}
}

// TestCanvasImageTransfersColorAtScreenSize covers the real editor case, where
// the transfer is a full-screen surface. This is the exact size that failed
// when StretchDIBits was handed a large Go-heap buffer.
func TestCanvasImageTransfersColorAtScreenSize(t *testing.T) {
	const w, h = 1920, 1080
	memDC, cleanup, read := pixelReader(t, w, h)
	defer cleanup()

	c := &Canvas{hdc: memDC}
	magenta := RGB(255, 0, 255)
	c.Fill(Rect{Right: w, Bottom: h}, magenta)
	c.Image(Rect{Right: w, Bottom: h}, solidImage(w, h, 10, 200, 30))

	want := RGB(10, 200, 30)
	for _, p := range []struct{ x, y int32 }{{w / 2, h / 2}, {10, 10}, {w - 10, h - 10}} {
		if got := read(p.x, p.y); got != want {
			t.Fatalf("全屏尺寸传输失败于 (%d,%d): 取到 0x%06X, 期望 0x%06X (magenta=0x%06X 表示未绘制)",
				p.x, p.y, got, want, magenta)
		}
	}
}

// TestCanvasImageSubRect pins the sub-region transfer used to redraw only the
// selection at full brightness while the rest stays dimmed.
func TestCanvasImageSubRect(t *testing.T) {
	memDC, cleanup, read := pixelReader(t, 40, 40)
	defer cleanup()

	// Source is solid blue; copy its middle quarter into the top-left.
	c := &Canvas{hdc: memDC}
	c.Fill(Rect{Right: 40, Bottom: 40}, RGB(255, 0, 255))
	c.ImageSubRect(
		Rect{Left: 0, Top: 0, Right: 20, Bottom: 20},
		Rect{Left: 25, Top: 25, Right: 50, Bottom: 50},
		solidImage(100, 100, 0, 0, 255),
	)

	if got, want := read(10, 10), RGB(0, 0, 255); got != want {
		t.Fatalf("子区域传输失败: 取到 0x%06X, 期望 0x%06X", got, want)
	}
	// Outside the destination must be untouched.
	if got, want := read(30, 30), RGB(255, 0, 255); got != want {
		t.Fatalf("子区域传输越界: (30,30) 取到 0x%06X, 期望保持 0x%06X", got, want)
	}
}

// TestFillAlphaDims is the regression for the screenshot editor's dim overlay:
// AlphaBlend takes BLENDFUNCTION by value, and passing it by pointer made the
// call fail silently, so the desktop never dimmed.
func TestFillAlphaDims(t *testing.T) {
	memDC, cleanup, read := pixelReader(t, 64, 64)
	defer cleanup()

	c := &Canvas{hdc: memDC}
	c.Fill(Rect{Right: 64, Bottom: 64}, RGB(255, 255, 255))
	if got := read(32, 32); got != RGB(255, 255, 255) {
		t.Fatalf("底色应为白色, 实际 0x%06X", got)
	}

	c.FillAlpha(Rect{Right: 64, Bottom: 64}, RGB(0, 0, 0), 150)

	got := read(32, 32)
	if got == RGB(255, 255, 255) {
		t.Fatal("FillAlpha 未生效: 像素仍为纯白，遮罩没有绘制")
	}
	// A 150/255 black wash over white should land in a clearly dimmed range.
	if r := got & 0xFF; r < 60 || r > 140 {
		t.Fatalf("FillAlpha 遮罩强度异常: 取到 0x%06X (R=%d), 期望约 105", got, r)
	}
}

// TestContrastTextPicksReadableColour guards the taskbar's adaptive foreground:
// light backgrounds must get dark text and vice versa.
func TestContrastTextPicksReadableColour(t *testing.T) {
	cases := []struct {
		name string
		bg   uint32
		want uint32
	}{
		{"白色背景配黑字", RGB(255, 255, 255), RGB(0, 0, 0)},
		{"浅灰背景配黑字", RGB(200, 200, 200), RGB(0, 0, 0)},
		{"黑色背景配白字", RGB(0, 0, 0), RGB(255, 255, 255)},
		{"深色主题背景配白字", RGB(32, 33, 36), RGB(255, 255, 255)},
	}
	for _, c := range cases {
		if got := ContrastText(c.bg); got != c.want {
			t.Errorf("%s: ContrastText(0x%06X) = 0x%06X, 期望 0x%06X", c.name, c.bg, got, c.want)
		}
	}
}
