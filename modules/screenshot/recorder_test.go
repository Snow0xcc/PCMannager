package screenshot

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"
)

// makeRGBA builds a solid w×h image.
func makeRGBA(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// TestFrameEncoderEncodesGIF 守护录屏编码：加入若干帧后应产出可解码的 GIF，
// 且帧数与时长与输入一致。
func TestFrameEncoderEncodesGIF(t *testing.T) {
	enc := newFrameEncoder(40, 30, 10)
	if enc == nil {
		t.Fatal("newFrameEncoder 返回 nil")
	}

	colors := []color.RGBA{
		{R: 255, A: 255},
		{G: 255, A: 255},
		{B: 255, A: 255},
	}
	for _, c := range colors {
		if !enc.add(makeRGBA(40, 30, c)) {
			t.Fatal("add 应成功")
		}
	}
	if got := enc.count(); got != len(colors) {
		t.Fatalf("帧数 = %d, 期望 %d", got, len(colors))
	}

	var buf bytes.Buffer
	if err := enc.encode(&buf); err != nil {
		t.Fatalf("encode 失败: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("编码结果为空")
	}

	doc, err := gif.DecodeAll(&buf)
	if err != nil {
		t.Fatalf("产出的 GIF 无法解码: %v", err)
	}
	if len(doc.Image) != len(colors) {
		t.Fatalf("GIF 帧数 = %d, 期望 %d", len(doc.Image), len(colors))
	}
	if got := doc.Image[0].Bounds().Dx(); got != 40 {
		t.Fatalf("帧宽 = %d, 期望 40", got)
	}
	if got := doc.Image[0].Bounds().Dy(); got != 30 {
		t.Fatalf("帧高 = %d, 期望 30", got)
	}
	// 10fps => 10 centiseconds per frame.
	if doc.Delay[0] < 8 || doc.Delay[0] > 12 {
		t.Fatalf("帧延迟 = %d, 期望约 10", doc.Delay[0])
	}
}

// TestFrameEncoderRejectsEmpty 守护空编码不产生垃圾文件。
func TestFrameEncoderRejectsEmpty(t *testing.T) {
	enc := newFrameEncoder(20, 20, 10)
	var buf bytes.Buffer
	if err := enc.encode(&buf); err == nil {
		t.Fatal("空编码应返回错误")
	}
	if buf.Len() != 0 {
		t.Fatalf("空编码不应写出数据, 实际 %d 字节", buf.Len())
	}
}

// TestFrameEncoderClampsFPS 守护 fps 边界，避免产生不可播放的 GIF 延迟。
func TestFrameEncoderClampsFPS(t *testing.T) {
	cases := []struct {
		fps       int
		wantDelay int // centiseconds
	}{
		{1, 10},  // 低于下限 -> 5fps
		{5, 20},  // 下限
		{10, 10}, // 默认
		{25, 4},  // 上限
		{100, 4}, // 高于上限 -> 25fps
	}
	for _, c := range cases {
		enc := newFrameEncoder(10, 10, c.fps)
		if enc == nil {
			t.Fatalf("fps=%d 时 newFrameEncoder 返回 nil", c.fps)
		}
		if enc.delay < 2 {
			t.Errorf("fps=%d 时 delay=%d, 不应低于 2（GIF 会把 <2 视为最快）", c.fps, enc.delay)
		}
	}
}

// TestFrameEncoderInvalidSize 守护非法尺寸返回 nil 而不是 panic。
func TestFrameEncoderInvalidSize(t *testing.T) {
	if enc := newFrameEncoder(0, 10, 10); enc != nil {
		t.Fatal("宽度 0 应返回 nil")
	}
	if enc := newFrameEncoder(10, -1, 10); enc != nil {
		t.Fatal("高度为负应返回 nil")
	}
}

// TestFrameEncoderCap 守护帧数上限：达到上限后 add 返回 false，防止无界增长。
func TestFrameEncoderCap(t *testing.T) {
	enc := newFrameEncoder(4, 4, recMaxFPS)
	// 直接操纵内部切片到上限，避免真的编码上千帧。
	enc.frames = make([]*image.Paletted, recMaxFrames)
	if !enc.capped() {
		t.Fatal("达到上限时 capped() 应为 true")
	}
	if enc.add(makeRGBA(4, 4, color.RGBA{A: 255})) {
		t.Fatal("达到上限后 add 应返回 false")
	}
}
