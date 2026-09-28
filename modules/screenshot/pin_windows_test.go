//go:build windows

package screenshot

import "testing"

// TestClampZoom 守护缩放因子钳制：低于下限/高于上限时收回到边界。
func TestClampZoom(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0.5, 0.5},
		{1.0, 1.0},
		{3.0, 3.0},
		{0.01, pinZoomMin},  // 过小 → 下限
		{100.0, pinZoomMax}, // 过大 → 上限
		{-1.0, pinZoomMin},
	}
	for _, c := range cases {
		if got := clampZoom(c.in); got != c.want {
			t.Errorf("clampZoom(%v) = %v, 期望 %v", c.in, got, c.want)
		}
	}
}

// TestZoomAnchor 守护指针锚点换算：同一图像点在不同缩放下应换算出
// 成比例的客户区坐标（这是“以指针为中心缩放”的数学基础）。
func TestZoomAnchor(t *testing.T) {
	// 图像点 ax=50：1x 下在客户区 50px，2x 下在 100px。
	if got := zoomAnchor(50, 1.0); got != 50 {
		t.Errorf("zoomAnchor(50, 1) = %v, 期望 50", got)
	}
	if got := zoomAnchor(100, 2.0); got != 50 {
		t.Errorf("zoomAnchor(100, 2) = %v, 期望 50", got)
	}
	// 零/负 zoom 防御：不应除零 panic。
	if got := zoomAnchor(50, 0); got != 50 {
		t.Errorf("zoomAnchor(50, 0) 应按 1x 处理 = 50, 实际 %v", got)
	}
}

// TestZoomedOrigin 守护缩放后窗口原点：锚点必须仍落在指针下。
func TestZoomedOrigin(t *testing.T) {
	// 指针在屏幕 (300,_)；图像点 ax=50 在 2x 下偏移 100px，
	// 窗口原点应为 300-100=200，使 ax 恰好在指针下。
	if got := zoomedOrigin(300, 50, 2.0); got != 200 {
		t.Errorf("zoomedOrigin(300, 50, 2) = %d, 期望 200", got)
	}
	// 缩小到 0.5x：ax=50 偏移 25px，原点应为 275。
	if got := zoomedOrigin(300, 50, 0.5); got != 275 {
		t.Errorf("zoomedOrigin(300, 50, 0.5) = %d, 期望 275", got)
	}
}

// TestZoomedOriginKeepsAnchorUnderCursor 端到端验证缩放数学：
// 指针下的图像点，在缩放前后都应落在指针的屏幕位置上。
func TestZoomedOriginKeepsAnchorUnderCursor(t *testing.T) {
	const (
		cursorScreenX = int32(500)
		cursorClientX = 120.0 // 指针在窗口内的位置
		oldZoom       = 1.0
		newZoom       = 2.5
	)

	ax := zoomAnchor(cursorClientX, oldZoom)
	newLeft := zoomedOrigin(cursorScreenX, ax, newZoom)

	// 缩放后指针相对窗口的偏移 = cursorScreenX - newLeft，
	// 应等于 ax * newZoom（即同一图像点仍在指针下）。
	offset := float64(cursorScreenX - newLeft)
	if d := offset - ax*newZoom; d < -0.51 || d > 0.51 {
		t.Errorf("缩放后锚点偏移 %.2f != ax*newZoom %.2f（指针下的图像点漂移了）",
			offset, ax*newZoom)
	}
}

// TestWheelStep 守护滚轮增量累积：高分滚轮的平滑小增量必须攒满 ±120
// 才产生一个整档，且余量保留（不丢步）。
func TestWheelStep(t *testing.T) {
	// 两个 60 的增量 = 恰好 1 档 → 1 步 + 余 0。
	acc, steps := wheelStep(0, 60)
	if steps != 0 || acc != 60 {
		t.Fatalf("首坎 60 不应产生步进, 实际 %d 步余 %d", steps, acc)
	}
	acc, steps = wheelStep(acc, 60)
	if steps != 1 || acc != 0 {
		t.Fatalf("累计 120 应产生 1 步余 0, 实际 %d 步余 %d", steps, acc)
	}
	// 再 60 → 余 60；再 60 → 又 1 步（验证余量保留、不丢步）。
	acc, steps = wheelStep(acc, 60)
	if steps != 0 || acc != 60 {
		t.Fatalf("再 60 应余 60, 实际 %d 步余 %d", steps, acc)
	}
	acc, steps = wheelStep(acc, 60)
	if steps != 1 || acc != 0 {
		t.Errorf("余量累积应再产生 1 步, 实际 %d 步余 %d", steps, acc)
	}

	// 反向：-180 = 1 步反向 + 余 -60。
	acc, steps = wheelStep(0, -180)
	if steps != -1 || acc != -60 {
		t.Errorf("-180 应产生 -1 步余 -60, 实际 %d 步余 %d", steps, acc)
	}

	// 大增量一次进多档。
	_, steps = wheelStep(0, 360)
	if steps != 3 {
		t.Errorf("360 应产生 3 步, 实际 %d", steps)
	}

	// 零增量不动。
	acc, steps = wheelStep(30, 0)
	if steps != 0 || acc != 30 {
		t.Errorf("零增量不应产生步进或改变余量, 实际 %d 步余 %d", steps, acc)
	}
}

// TestPinSlotAccounting 守护贴图槽位：预占→释放（幂等）→可再次预占。
func TestPinSlotAccounting(t *testing.T) {
	before := pinnedCount()
	if err := pinReserve(); err != nil {
		t.Fatalf("未达上限时应可预占: %v", err)
	}
	if pinnedCount() != before+1 {
		t.Fatalf("预占后计数 = %d, 期望 %d", pinnedCount(), before+1)
	}
	pinRelease()
	pinRelease() // 幂等：不减成负数
	if pinnedCount() != before {
		t.Fatalf("释放后计数 = %d, 期望 %d", pinnedCount(), before)
	}
}
