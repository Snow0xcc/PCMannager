//go:build windows

package screenshot

import (
	"image"
	"testing"
	"time"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// imagePoint 是测试里构造 image.Point 的简写，避免直接引入 image 包名冲突。
func imagePoint(x, y int) image.Point { return image.Pt(x, y) }

// toolbarForTest builds an editorState with a fixed selection, suitable for
// exercising the toolbar geometry without a window (ClientRect on a nil window
// returns the zero rect, which buttonRect tolerates).
func toolbarForTest() *editorState {
	return &editorState{
		tool:    -1,
		hoverID: -1,
		sel:     winui.Rect{Left: 100, Top: 100, Right: 300, Bottom: 200},
		origin:  imagePoint(0, 0),
	}
}

// TestToolbarWidthIncludesHandle 守护行宽计算：拖动把手必须占行首一个槽位，
// 否则把手会与最左侧按钮重叠、点击错位。
func TestToolbarWidthIncludesHandle(t *testing.T) {
	w3 := edToolbarWidth(3)
	w9 := edToolbarWidth(9)

	want3 := int32(edHandleW + edBtnGap + 3*(edBtnW+edBtnGap) - edBtnGap)
	if w3 != want3 {
		t.Fatalf("3 按钮行宽 = %d, 期望 %d", w3, want3)
	}
	if w9 <= w3 {
		t.Fatalf("9 按钮行宽 %d 应大于 3 按钮行宽 %d", w9, w3)
	}
}

// TestButtonRectLaysOutRightToLeft 守护按钮从右向左排：索引 0 是最右侧按钮
// （框选结束时光标所在处），且相邻按钮不重叠。
func TestButtonRectLaysOutRightToLeft(t *testing.T) {
	e := toolbarForTest()
	n := len(e.buttons())

	first := e.buttonRect(0, e.sel)
	last := e.buttonRect(n-1, e.sel)
	if first.Right <= last.Right {
		t.Fatalf("按钮 0（右缘 %d）应在最后一个按钮（右缘 %d）右侧", first.Right, last.Right)
	}

	// 相邻按钮间距 = edBtnGap，无重叠。
	for i := 1; i < n; i++ {
		prev := e.buttonRect(i-1, e.sel)
		cur := e.buttonRect(i, e.sel)
		if cur.Right+edBtnGap != prev.Left {
			t.Fatalf("按钮 %d 与 %d 间距错误: cur.Right=%d + gap=%d != prev.Left=%d",
				i-1, i, cur.Right, edBtnGap, prev.Left)
		}
	}
}

// TestHandleRectIsLeftmostSlot 守护把手位置：必须紧贴最左按钮的左侧，
// 且与按钮行同高（命中测试依赖这一点）。
func TestHandleRectIsLeftmostSlot(t *testing.T) {
	e := toolbarForTest()
	h := e.handleRect(e.sel)
	lastBtn := e.buttonRect(len(e.buttons())-1, e.sel)

	if h.Right+edBtnGap != lastBtn.Left {
		t.Fatalf("把手右缘 %d + 间隙 %d 应等于最左按钮左缘 %d",
			h.Right, edBtnGap, lastBtn.Left)
	}
	if h.Top != lastBtn.Top || h.Bottom != lastBtn.Bottom {
		t.Fatalf("把手应与按钮行同高: handle=(%d,%d) btn=(%d,%d)",
			h.Top, h.Bottom, lastBtn.Top, lastBtn.Bottom)
	}
}

// TestToolbarDragOffsetMovesRow 守护拖动偏移确实移动整行，
// 且不会把行移出窗口可命中范围（钳制逻辑）。
func TestToolbarDragOffsetMovesRow(t *testing.T) {
	e := toolbarForTest()
	before := e.buttonRect(0, e.sel)

	e.mu.Lock()
	e.toolbarDX = 120
	e.toolbarDY = 60
	e.mu.Unlock()

	after := e.buttonRect(0, e.sel)
	if after.Left <= before.Left {
		t.Fatalf("右移后按钮左缘 %d 应大于原值 %d", after.Left, before.Left)
	}
	if after.Top <= before.Top {
		t.Fatalf("下移后按钮顶缘 %d 应大于原值 %d", after.Top, before.Top)
	}

	// 巨大偏移必须被钳回窗口内（nil 窗口 => ClientRect 为零矩形，即 0,0,0,0），
	// 表现为按钮不会变成负坐标或超出可点区域。
	e.mu.Lock()
	e.toolbarDX = 1 << 20
	e.toolbarDY = 1 << 20
	e.mu.Unlock()
	clamped := e.buttonRect(0, e.sel)
	if clamped.Left < 0 || clamped.Top < 0 {
		t.Fatalf("钳制后按钮坐标不能为负: left=%d top=%d", clamped.Left, clamped.Top)
	}
}

// TestHoverUpdateDoesNotDeadlock 回归测试：onMove/onUp 曾在持有 mu 时调用
// hitButton（内部再次拿同一把锁），Go 的 sync.Mutex 不可重入，鼠标一动就死锁。
// 这里以与 onMove 相同的持锁顺序调用无锁核心 hitButtonLocked，能返回即说明
// 调用约定正确；若有人改回直接调 hitButton，本测试会因死锁超时而失败。
func TestHoverUpdateDoesNotDeadlock(t *testing.T) {
	e := toolbarForTest()
	done := make(chan int, 1)
	go func() {
		e.mu.Lock()
		// 模拟 onMove 的调用序列：持锁下用无锁核心重算 hover。
		h := e.hitButtonLocked(150, 150, e.sel)
		e.hoverID = h
		e.mu.Unlock()
		done <- h
	}()
	select {
	case <-done:
		// 成功返回即通过。
	case <-time.After(2 * time.Second):
		t.Fatal("持锁调用 hitButtonLocked 死锁：可能误用了需要拿锁的 hitButton")
	}
}

// TestToolbarDragLifecycle 走一遍完整的把手拖动状态机：
// 按下（进入拖动）→ 移动（偏移变化）→ 松开（退出拖动且 hover 重算）。
func TestToolbarDragLifecycle(t *testing.T) {
	e := toolbarForTest()
	e.mode = edModeCapture

	// 把手矩形中心。
	h := e.handleRect(e.sel)
	hx := (h.Left + h.Right) / 2
	hy := (h.Top + h.Bottom) / 2

	// onDown 在把手上 => 进入拖动。
	e.onDown(hx, hy)
	e.mu.Lock()
	dragging := e.handleDrag
	e.mu.Unlock()
	if !dragging {
		t.Fatal("在把手上按下应进入拖动状态")
	}

	// 移动 30px => 偏移变化、锚点跟随。
	before := e.toolbarDX
	e.onMove(hx+30, hy+10)
	e.mu.Lock()
	afterDX := e.toolbarDX
	anchorMoved := e.handleAnchorX == hx+30
	e.mu.Unlock()
	if afterDX != before+30 {
		t.Fatalf("拖动后偏移 = %d, 期望 %d", afterDX, before+30)
	}
	if !anchorMoved {
		t.Fatal("拖动锚点应跟随光标，否则后续移动会跳变")
	}

	// 松开 => 退出拖动。
	e.onUp(hx+30, hy+10)
	e.mu.Lock()
	dragging = e.handleDrag
	e.mu.Unlock()
	if dragging {
		t.Fatal("松开后应退出拖动状态")
	}
}

// TestToolbarButtonsEveryModeHaveTooltips 守护每个模式的按钮都带非空中文提示，
// 且贴图按钮只出现在裁剪模式。
func TestToolbarButtonsEveryModeHaveTooltips(t *testing.T) {
	for _, mode := range []edMode{edModeCapture, edModeRecord, edModeScroll} {
		e := toolbarForTest()
		e.mode = mode
		for _, b := range e.buttons() {
			if b.label == "" {
				t.Errorf("模式 %d 按钮 %d 缺少提示文本", mode, b.id)
			}
			hasPin := false
			if b.id == edBtnPin {
				hasPin = true
			}
			if hasPin && mode != edModeCapture {
				t.Errorf("贴图按钮不应出现在模式 %d", mode)
			}
		}
	}
}
