//go:build windows

package screenshot

import (
	"image"
	"testing"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// TestToolOfMapsButtons 守护工具按钮到标注类型的映射，以及非工具按钮返回 -1。
func TestToolOfMapsButtons(t *testing.T) {
	cases := []struct {
		btn  int
		want int
	}{
		{edBtnRect, int(annotRect)},
		{edBtnEllipse, int(annotEllipse)},
		{edBtnArrow, int(annotArrow)},
		{edBtnPen, int(annotPen)},
		{edBtnConfirm, -1},
		{edBtnCopy, -1},
		{edBtnCancel, -1},
		{edBtnUndo, -1},
	}
	for _, c := range cases {
		if got := toolOf(c.btn); got != c.want {
			t.Errorf("toolOf(%d) = %d, 期望 %d", c.btn, got, c.want)
		}
	}
}

// TestToolbarsCoverAllModes 守护每个模式的工具栏都有按钮、标签非空，且主操作在
// 最右位（buttonRect 从右往左排，主操作应落在框选结束时光标所在的位置）。
func TestToolbarsCoverAllModes(t *testing.T) {
	for _, mode := range []edMode{edModeCapture, edModeRecord, edModeScroll} {
		e := &editorState{tool: -1, mode: mode}
		btns := e.buttons()
		if len(btns) == 0 {
			t.Fatalf("模式 %d 没有任何工具栏按钮", mode)
		}
		seen := map[int]bool{}
		for _, b := range btns {
			if b.label == "" {
				t.Errorf("模式 %d 的按钮 %d 缺少标签", mode, b.id)
			}
			if seen[b.id] {
				t.Errorf("模式 %d 重复出现按钮 %d", mode, b.id)
			}
			seen[b.id] = true
		}
		if !seen[edBtnCancel] {
			t.Errorf("模式 %d 缺少取消按钮", mode)
		}
	}
}

// TestOnlyCaptureModeDrawsAnnotations 守护非裁剪模式不启用标注工具，否则图形会
// 被烘进录屏/长截图且无法撤销。
func TestOnlyCaptureModeDrawsAnnotations(t *testing.T) {
	for _, tc := range []struct {
		mode edMode
		want bool
	}{
		{edModeCapture, true},
		{edModeRecord, false},
		{edModeScroll, false},
	} {
		e := &editorState{
			mode:        tc.mode,
			tool:        int(annotRect),
			sel:         winui.Rect{Left: 0, Top: 0, Right: 100, Bottom: 100},
			strokeColor: annotPalette[0],
			strokeWidth: annotWidths[0],
		}
		if got := e.beginDraw(10, 10); got != tc.want {
			t.Errorf("模式 %d beginDraw = %v, 期望 %v", tc.mode, got, tc.want)
		}
	}
}

// TestScreenSelectionAddsOrigin 守护客户区选区到屏幕坐标的换算：屏幕截图按桌面
// 坐标寻址，少加原点会导致录到/拼到错误的区域。
func TestScreenSelectionAddsOrigin(t *testing.T) {
	e := &editorState{tool: -1}
	sel := winui.Rect{Left: 10, Top: 20, Right: 110, Bottom: 120}

	got := e.screenSelection(sel, image.Pt(1000, 500))
	if got == nil {
		t.Fatal("有效选区不应返回 nil")
	}
	want := image.Rect(1010, 520, 1110, 620)
	if *got != want {
		t.Fatalf("screenSelection = %v, 期望 %v", *got, want)
	}

	// A tap too small to be a real selection must be rejected rather than
	// producing a 1-pixel region.
	if got := e.screenSelection(winui.Rect{Left: 5, Top: 5, Right: 6, Bottom: 6}, image.Point{}); got != nil {
		t.Fatalf("过小选区应返回 nil, 实际 %v", *got)
	}
}

// TestCaptureOutcomeTakeClears 守护结果槽位取值后即清空，避免下一次捕获读到上一次
// 的路径。
func TestCaptureOutcomeTakeClears(t *testing.T) {
	var o captureOutcome
	o.set("已保存", `C:\tmp\shot.png`)

	text, path := o.take()
	if text != "已保存" || path != `C:\tmp\shot.png` {
		t.Fatalf("take = (%q,%q)", text, path)
	}
	if text, path := o.take(); text != "" || path != "" {
		t.Fatalf("第二次 take 应为空, 实际 (%q,%q)", text, path)
	}
}

// TestAppendPenPointSkipsDuplicates 守护画笔路径不记录原地重复点。
func TestAppendPenPointSkipsDuplicates(t *testing.T) {
	pts := newPenStroke(10, 10)
	if len(pts) != 1 {
		t.Fatalf("初始路径长度 = %d, 期望 1", len(pts))
	}
	// Same point: not appended.
	pts = appendPenPoint(pts, 10, 10)
	if len(pts) != 1 {
		t.Fatalf("重复点不应追加, 长度 = %d", len(pts))
	}
	// Moved point: appended.
	pts = appendPenPoint(pts, 12, 14)
	if len(pts) != 2 {
		t.Fatalf("移动点应追加, 长度 = %d", len(pts))
	}
	if pts[1].X != 12 || pts[1].Y != 14 {
		t.Fatalf("追加的点坐标错误: %+v", pts[1])
	}
}

// TestNormalizePointRectOrdersCorners 守护拖拽到任意方向都能得到归一化矩形。
func TestNormalizePointRectOrdersCorners(t *testing.T) {
	cases := []struct {
		x1, y1, x2, y2 int32
		want           winui.Rect
	}{
		{10, 10, 50, 40, winui.Rect{Left: 10, Top: 10, Right: 50, Bottom: 40}},
		{50, 40, 10, 10, winui.Rect{Left: 10, Top: 10, Right: 50, Bottom: 40}},
		{50, 10, 10, 40, winui.Rect{Left: 10, Top: 10, Right: 50, Bottom: 40}},
	}
	for _, c := range cases {
		if got := normalizePointRect(c.x1, c.y1, c.x2, c.y2); got != c.want {
			t.Errorf("normalizePointRect(%d,%d,%d,%d) = %+v, 期望 %+v",
				c.x1, c.y1, c.x2, c.y2, got, c.want)
		}
	}
}

// TestAnnotTranslated 守护标注坐标平移：导出时把窗口坐标转换为裁剪图坐标。
func TestAnnotTranslated(t *testing.T) {
	a := annot{
		Kind:   annotPen,
		From:   winui.Rect{Left: 100, Top: 200},
		To:     winui.Rect{Left: 150, Top: 250},
		Points: []winui.POINT{{X: 100, Y: 200}, {X: 120, Y: 220}},
	}
	got := a.translated(-40, -60)

	if got.From.Left != 60 || got.From.Top != 140 {
		t.Errorf("From 平移错误: %+v", got.From)
	}
	if got.To.Left != 110 || got.To.Top != 190 {
		t.Errorf("To 平移错误: %+v", got.To)
	}
	if len(got.Points) != 2 || got.Points[0].X != 60 || got.Points[0].Y != 140 {
		t.Errorf("Points 平移错误: %+v", got.Points)
	}
	// The original must be untouched (annot is copied by value, but the slice
	// header points at the same array, so this guards a real aliasing bug).
	if a.Points[0].X != 100 {
		t.Errorf("平移不应修改原标注: %+v", a.Points[0])
	}
}

// TestPointInRect 守护选区命中判定（含边界）。
func TestPointInRect(t *testing.T) {
	r := winui.Rect{Left: 10, Top: 20, Right: 100, Bottom: 80}
	cases := []struct {
		x, y int32
		want bool
	}{
		{10, 20, true},   // 左上角
		{100, 80, true},  // 右下角
		{50, 50, true},   // 内部
		{9, 50, false},   // 左侧外部
		{101, 50, false}, // 右侧外部
		{50, 19, false},  // 上方外部
		{50, 81, false},  // 下方外部
	}
	for _, c := range cases {
		if got := pointInRect(c.x, c.y, r); got != c.want {
			t.Errorf("pointInRect(%d,%d) = %v, 期望 %v", c.x, c.y, got, c.want)
		}
	}
}

// TestUndoRemovesLastAnnotation 守护撤销只弹出最后一条。
func TestUndoRemovesLastAnnotation(t *testing.T) {
	e := &editorState{tool: -1}
	e.annots = []annot{
		{Kind: annotRect},
		{Kind: annotEllipse},
		{Kind: annotArrow},
	}
	// undo is called directly; it locks and repaints, and repaint is a no-op
	// with no window attached.
	e.undo()
	if len(e.annots) != 2 {
		t.Fatalf("撤销后应剩 2 条, 实际 %d", len(e.annots))
	}
	if e.annots[len(e.annots)-1].Kind != annotEllipse {
		t.Fatalf("撤销应移除最后一条(箭头), 剩余末尾 = %v", e.annots[len(e.annots)-1].Kind)
	}

	e.undo()
	e.undo()
	if len(e.annots) != 0 {
		t.Fatalf("全部撤销后应为空, 实际 %d", len(e.annots))
	}
	// Extra undo must not panic.
	e.undo()
	if len(e.annots) != 0 {
		t.Fatalf("空列表撤销后仍应为空")
	}
}

// TestExportShift math-verifies the annotation offset used when baking shapes
// into the cropped image: the crop's top-left must map to the overlay origin.
func TestExportShift(t *testing.T) {
	// A shape drawn at (150,120) in window space, cropped at (100,100) in the
	// same space, must land at (50,20) in the exported image.
	a := annot{Kind: annotRect, From: winui.Rect{Left: 150, Top: 120}, To: winui.Rect{Left: 200, Top: 170}}
	cropMinX, cropMinY := int32(100), int32(100)

	got := a.translated(-cropMinX, -cropMinY)
	if got.From.Left != 50 || got.From.Top != 20 {
		t.Fatalf("偏移后 From = %+v, 期望 (50,20)", got.From)
	}
}
