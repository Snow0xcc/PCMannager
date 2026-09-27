//go:build windows

package screenshot

import (
	"image"
	"testing"

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
