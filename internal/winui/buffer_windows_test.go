//go:build windows

package winui

import (
	"testing"
)

// makeBufferTestWindow 创建一个真实窗口供双缓冲测试使用。CreateWindowExW 依赖
// 交互桌面；拿不到窗口时跳过（环境不支持，不是代码缺陷）。
func makeBufferTestWindow(t *testing.T) *Window {
	t.Helper()
	w, err := NewWindow("PCMBufferTest", WS_POPUP, 0, Invalid)
	if err != nil || !w.HWND().Valid() {
		t.Skipf("无法创建测试窗口（可能无交互桌面）: %v", err)
	}
	t.Cleanup(w.Destroy)
	// 双缓冲按客户区尺寸建缓冲，先给窗口一个非零客户区。
	if err := MoveWindow(w.HWND(), 10, 10, 64, 48, false); err != nil {
		t.Skipf("MoveWindow 失败: %v", err)
	}
	return w
}

// TestBeginBufferedPaintReturnsUsableCanvas 验证主路径：真实窗口上返回非零 DC
// 与非 nil 的 done，done 调用一次不 panic。
func TestBeginBufferedPaintReturnsUsableCanvas(t *testing.T) {
	w := makeBufferTestWindow(t)

	c, done, err := BeginBufferedPaint(w.HWND())
	if err != nil {
		t.Fatalf("BeginBufferedPaint 失败: %v", err)
	}
	if c == nil || c.DC() == 0 {
		t.Fatal("BeginBufferedPaint 返回了空 Canvas 或零 DC")
	}
	if done == nil {
		t.Fatal("done 闭包为 nil，调用方 defer 会 panic")
	}
	done()
}

// TestBeginBufferedPaintDoneIdempotent 覆盖 done 的幂等保护：defer 与显式调用
// 并存时只生效一次，不二次上屏、不重复释放 GDI 对象。
func TestBeginBufferedPaintDoneIdempotent(t *testing.T) {
	w := makeBufferTestWindow(t)

	c, done, err := BeginBufferedPaint(w.HWND())
	if err != nil {
		t.Fatalf("BeginBufferedPaint 失败: %v", err)
	}
	if c == nil {
		t.Fatal("Canvas 为 nil")
	}
	done()
	done() // 第二次必须是 no-op
}

// TestBeginBufferedPaintReusable 是"无句柄泄漏"的间接验证：无法直接数句柄，
// 但连续多轮建立/上屏若泄漏 GDI 对象，Windows 会在 GDI 句柄配额（默认 10000）
// 耗尽时开始失败，这里跑到足以让粗漏暴露的轮数。
func TestBeginBufferedPaintReusable(t *testing.T) {
	w := makeBufferTestWindow(t)

	const rounds = 64
	for i := 0; i < rounds; i++ {
		c, done, err := BeginBufferedPaint(w.HWND())
		if err != nil {
			t.Fatalf("第 %d 轮 BeginBufferedPaint 失败: %v", i+1, err)
		}
		if c == nil || c.DC() == 0 || done == nil {
			t.Fatalf("第 %d 轮返回非法结果", i+1)
		}
		// 每轮往缓冲里画一点，确保 DC 真的可用（GDI 内部句柄失效会在这里炸）。
		c.Fill(Rect{Right: 8, Bottom: 8}, RGB(255, 0, 255))
		done()
	}
}

// TestBeginBufferedPaintInvalidWindow 覆盖非法输入：零句柄必须返回错误与空
// done，调用方 defer noopDone 不会 panic。
func TestBeginBufferedPaintInvalidWindow(t *testing.T) {
	c, done, err := BeginBufferedPaint(Invalid)
	if err == nil {
		t.Fatal("零句柄应返回错误")
	}
	if c != nil {
		t.Fatal("失败时 Canvas 应为 nil")
	}
	if done == nil {
		t.Fatal("失败时 done 仍应为非 nil 的 no-op，方便统一 defer")
	}
	done() // 不得 panic
}
