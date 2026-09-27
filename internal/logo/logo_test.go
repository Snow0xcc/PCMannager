package logo

import (
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestRenderProducesVisibleMark 守护渲染输出非空且确有像素被着色：
// 一张全透明的图意味着几何完全没落到画布上（例如多边形顶点算错）。
func TestRenderProducesVisibleMark(t *testing.T) {
	for _, size := range []int{16, 32, 64, 256} {
		img := Render(size)
		if img == nil {
			t.Fatalf("Render(%d) = nil", size)
		}
		b := img.Bounds()
		if b.Dx() != size || b.Dy() != size {
			t.Fatalf("Render(%d) 尺寸 = %dx%d", size, b.Dx(), b.Dy())
		}
		painted := 0
		blue := 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := img.RGBAAt(x, y)
				if c.A != 0 {
					painted++
					// 品牌蓝：B 明显高于 R 的蓝色系像素（含深蓝机身）。
					if c.B > c.R && c.B > 0x60 {
						blue++
					}
				}
			}
		}
		if painted == 0 {
			t.Fatalf("Render(%d) 没有任何像素被绘制", size)
		}
		// 蓝色像素必须占已绘制像素的多数（蝴蝶主体是蓝的）。
		if blue*2 < painted {
			t.Fatalf("Render(%d) 蓝色像素占比过低: %d/%d", size, blue, painted)
		}
	}
}

// TestRenderSmallSizeLegible 守护 16px 下标志仍可辨识：
// 至少要有蓝色翅膀与深色机身两种颜色同时存在。
func TestRenderSmallSizeLegible(t *testing.T) {
	img := Render(16)
	if img == nil {
		t.Fatal("Render(16) = nil")
	}
	hasBlue, hasBody := false, false
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if c.A == 0 {
				continue
			}
			if c.B > c.R && c.B > 0x60 {
				hasBlue = true
			}
			// 机身深蓝 #1C3A70：暗且偏蓝。
			if c.R < 0x40 && c.G < 0x60 && c.B > 0x50 {
				hasBody = true
			}
		}
	}
	if !hasBlue {
		t.Fatal("16px 下没有蓝色翅膀像素")
	}
	if !hasBody {
		t.Fatal("16px 下没有深色机身像素")
	}
}

// TestRenderRejectsInvalid 守护非法尺寸返回 nil 而不是 panic。
func TestRenderRejectsInvalid(t *testing.T) {
	for _, size := range []int{0, -1, -100} {
		if img := Render(size); img != nil {
			t.Fatalf("Render(%d) 应返回 nil", size)
		}
	}
}

// TestMirrorXKeepsBounds 守护镜像折返不出界（翅膀几何沿 x=50 对称展开）。
func TestMirrorXKeepsBounds(t *testing.T) {
	poly := bezierPath(upperWing, 8)
	for i, p := range mirrorX(poly) {
		if p.x < 0 || p.x > 100 || p.y < 0 || p.y > 100 {
			t.Fatalf("镜像点 %d 越界: (%.1f, %.1f)", i, p.x, p.y)
		}
	}
}

// TestWingColorIsBrandBlue 锁定品牌色，防止无意改动后与面板强调色脱节。
func TestWingColorIsBrandBlue(t *testing.T) {
	want := color.RGBA{R: 0x4f, G: 0x8c, B: 0xff, A: 0xff}
	if wingBlue != want {
		t.Fatalf("wingBlue = %+v, 期望品牌蓝 %+v", wingBlue, want)
	}
}

// TestMain 辅助：设置环境变量 PCM_LOGO_DUMP 时把各尺寸渲染结果写成 PNG，
// 便于人工目检蝴蝶形状（正常运行时为 no-op）。
func TestMain(m *testing.M) {
	code := m.Run()
	if dir := os.Getenv("PCM_LOGO_DUMP"); dir != "" {
		for _, size := range []int{16, 32, 64, 128} {
			img := Render(size)
			if img == nil {
				continue
			}
			f, err := os.Create(filepath.Join(dir, "logo_"+itoa(size)+".png"))
			if err != nil {
				continue
			}
			_ = png.Encode(f, img)
			_ = f.Close()
		}
	}
	os.Exit(code)
}

// itoa 是测试内的小整数转字符串，避免仅为测试引入 strconv。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 && i > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
