package screenshot

import (
	"image"
	"image/color"
	"testing"
)

// paintBand fills rows [top,bottom) of img with c.
func paintBand(img *image.RGBA, top, bottom int, c color.RGBA) {
	for y := top; y < bottom; y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// TestStitchAppendsOnlyNewRows 守护核心拼接语义：第二帧与第一帧有重叠时，
// 只追加重叠之下新增的行，而不是整帧堆叠。
func TestStitchAppendsOnlyNewRows(t *testing.T) {
	const w = 60
	// 用「每 10 行一个色带、且色带颜色由行号决定」的图案模拟可辨认内容。
	tall := func(shiftRows int) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, w, 200))
		for y := 0; y < 200; y++ {
			// 行号（含 shift）决定颜色，滚动后同一内容行颜色不变。
			v := uint8((y + shiftRows) % 200)
			paintBand(img, y, y+1, color.RGBA{R: v, G: v, B: v, A: 255})
		}
		return img
	}

	first := tall(0)
	s := newScrollStitcher(first)
	if s == nil {
		t.Fatal("newScrollStitcher 返回 nil")
	}
	if got := s.height(); got != 200 {
		t.Fatalf("初始高度 = %d, 期望 200", got)
	}

	// 内容向上滚动了 30 行：新帧显示的内容从原第 30 行开始。
	second := tall(30)
	res := s.append(second)
	if !res.Matched {
		t.Fatal("内容重叠时应匹配成功")
	}
	if res.Advanced != 30 {
		t.Fatalf("新增行数 = %d, 期望 30", res.Advanced)
	}
	if got := s.height(); got != 230 {
		t.Fatalf("拼接后高度 = %d, 期望 230", got)
	}
}

// TestStitchNoMotionAddsNothing 守护无滚动时不重复追加。
func TestStitchNoMotionAddsNothing(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 120))
	paintBand(img, 0, 60, color.RGBA{R: 10, A: 255})
	paintBand(img, 60, 120, color.RGBA{B: 200, A: 255})

	s := newScrollStitcher(img)
	res := s.append(cloneRGBA(img))
	if !res.Matched {
		t.Fatal("同一帧应匹配成功")
	}
	if res.Advanced != 0 {
		t.Fatalf("无滚动时新增 = %d, 期望 0", res.Advanced)
	}
	if got := s.height(); got != 120 {
		t.Fatalf("高度不应变化, 实际 %d", got)
	}
}

// TestStitchReportsLostOverlap 守护「重叠丢失」场景：滚动超过一屏时无法拼接，
// 必须报告 Matched=false 而不是伪造内容。
func TestStitchReportsLostOverlap(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 40, 100))
	paintBand(a, 0, 100, color.RGBA{R: 255, A: 255})

	b := image.NewRGBA(image.Rect(0, 0, 40, 100))
	paintBand(b, 0, 100, color.RGBA{B: 255, A: 255}) // 完全不同的内容

	s := newScrollStitcher(a)
	res := s.append(b)
	if res.Matched {
		t.Fatal("内容完全不同时不应报告匹配")
	}
	if res.Advanced != 0 {
		t.Fatalf("不匹配时不应追加, 实际 %d", res.Advanced)
	}
	if got := s.height(); got != 100 {
		t.Fatalf("不匹配时高度不应变化, 实际 %d", got)
	}
}

// TestStitchRejectsWidthChange 守护宽度变化时安全退出（窗口被改变大小）。
func TestStitchRejectsWidthChange(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 40, 100))
	b := image.NewRGBA(image.Rect(0, 0, 50, 100))

	s := newScrollStitcher(a)
	res := s.append(b)
	if res.Matched || res.Advanced != 0 {
		t.Fatalf("宽度变化不应匹配/追加: matched=%v advanced=%d", res.Matched, res.Advanced)
	}
}

// TestFindStripOffset 守护定位函数：能找到条带在帧中的准确偏移，
// 且在多处重复时返回最靠上的匹配。
func TestFindStripOffset(t *testing.T) {
	// 带唯一标记的图案，标记每 25 行出现一次。
	frame := image.NewRGBA(image.Rect(0, 0, 30, 150))
	paintBand(frame, 0, 150, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	for _, y := range []int{10, 60, 110} {
		paintBand(frame, y, y+5, color.RGBA{R: 250, A: 255})
	}

	// 取第 10..15 行作为条带。
	strip := image.NewRGBA(image.Rect(0, 0, 30, 5))
	for y := 0; y < 5; y++ {
		for x := 0; x < 30; x++ {
			strip.SetRGBA(x, y, color.RGBA{R: 250, A: 255})
		}
	}

	off, ok := findStripOffset(frame, strip, 10)
	if !ok {
		t.Fatal("应找到条带")
	}
	// 三处标记内容相同，应从预期位置（10）直接命中，而不是碰运气。
	if off != 10 {
		t.Fatalf("偏移 = %d, 期望 10（从预期位置命中）", off)
	}
}

// TestFindStripOffsetPrefersExpected 守护歧义消除：内容重复出现多次时，
// 必须选择离上一帧位置最近的那个匹配，而不是最靠上的那个。
func TestFindStripOffsetPrefersExpected(t *testing.T) {
	// 纯色背景：整帧都一样，条带可在任意偏移匹配。
	frame := makeRGBA(20, 200, color.RGBA{R: 7, G: 7, B: 7, A: 255})
	strip := makeRGBA(20, 20, color.RGBA{R: 7, G: 7, B: 7, A: 255})

	// 声明它来自第 100 行：应返回 100，而不是 0。
	off, ok := findStripOffset(frame, strip, 100)
	if !ok {
		t.Fatal("纯色背景应能匹配")
	}
	if off != 100 {
		t.Fatalf("偏移 = %d, 期望 100（应取最接近预期的匹配）", off)
	}
}

// TestFindStripOffsetNoMatch 守护找不到条带时返回 false。
func TestFindStripOffsetNoMatch(t *testing.T) {
	frame := makeRGBA(20, 50, color.RGBA{A: 255})
	strip := makeRGBA(20, 5, color.RGBA{R: 255, A: 255})
	if _, ok := findStripOffset(frame, strip, 0); ok {
		t.Fatal("完全不同的条带不应匹配")
	}
}

// TestStitchToleratesSlightNoise 守护轻微色差仍能匹配（截图之间的编码舍入）。
func TestStitchToleratesSlightNoise(t *testing.T) {
	base := image.NewRGBA(image.Rect(0, 0, 30, 120))
	paintBand(base, 0, 120, color.RGBA{R: 100, G: 100, B: 100, A: 255})

	noisy := image.NewRGBA(image.Rect(0, 0, 30, 120))
	paintBand(noisy, 0, 120, color.RGBA{R: 102, G: 99, B: 101, A: 255}) // 轻微差异

	s := newScrollStitcher(base)
	res := s.append(noisy)
	if !res.Matched {
		t.Fatal("轻微色差应仍在容差内匹配")
	}
}

// TestCloneRGBADoesNotAlias 守护克隆不共享底层数组（否则拼接会污染原帧）。
func TestCloneRGBADoesNotAlias(t *testing.T) {
	a := makeRGBA(10, 10, color.RGBA{R: 10, A: 255})
	b := cloneRGBA(a)
	b.SetRGBA(0, 0, color.RGBA{R: 200, A: 255})
	if got := a.RGBAAt(0, 0).R; got != 10 {
		t.Fatalf("克隆修改影响了原图: R=%d", got)
	}
}
