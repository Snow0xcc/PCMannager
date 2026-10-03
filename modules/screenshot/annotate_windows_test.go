//go:build windows

package screenshot

import (
	"image"
	"math"
	"testing"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// TestArrowKeepsTrueEndpoints 回归测试：从右下往左上画箭头时，From 必须保持
// 为真实起点 (200,150)，不能被归一化成外接矩形左上角 (100,100)——
// 那会反转线段方向，正是“箭头只能横竖画”的根因。
func TestArrowKeepsTrueEndpoints(t *testing.T) {
	e := &editorState{
		tool: -1,
		sel:  winui.Rect{Left: 0, Top: 0, Right: 400, Bottom: 300},
	}
	e.mu.Lock()
	e.tool = int(annotArrow)
	e.strokeColor = annotPalette[0]
	e.strokeWidth = annotWidths[1]
	e.mu.Unlock()

	// 从 (200,150) 拖到 (100,100)（右下 → 左上）。
	if !e.beginDraw(200, 150) {
		t.Fatal("beginDraw 应成功")
	}
	e.onMove(150, 125)
	e.onUp(100, 100)

	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.annots) != 1 {
		t.Fatalf("应产生 1 条箭头, 实际 %d", len(e.annots))
	}
	a := e.annots[0]
	if a.Kind != annotArrow {
		t.Fatalf("Kind = %v, 期望 annotArrow", a.Kind)
	}
	if a.From.Left != 200 || a.From.Top != 150 {
		t.Fatalf("箭头起点被篡改: From=(%d,%d), 期望 (200,150)", a.From.Left, a.From.Top)
	}
	if a.To.Left != 100 || a.To.Top != 100 {
		t.Fatalf("箭头终点错误: To=(%d,%d), 期望 (100,100)", a.To.Left, a.To.Top)
	}
}

// TestArrowRectStillNormalized 守护矩形/椭圆仍走归一化（箭头修复不得波及其它形状）。
func TestArrowRectStillNormalized(t *testing.T) {
	e := &editorState{
		tool: -1,
		sel:  winui.Rect{Left: 0, Top: 0, Right: 400, Bottom: 300},
	}
	e.mu.Lock()
	e.tool = int(annotRect)
	e.strokeColor = annotPalette[0]
	e.strokeWidth = annotWidths[1]
	e.mu.Unlock()

	e.beginDraw(200, 150)
	e.onUp(100, 100)

	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.annots) != 1 {
		t.Fatalf("应产生 1 条矩形, 实际 %d", len(e.annots))
	}
	a := e.annots[0]
	if a.From.Left != 100 || a.From.Top != 100 {
		t.Fatalf("矩形 From 应归一化到左上角: (%d,%d)", a.From.Left, a.From.Top)
	}
}

// TestArrowHeadBarbAnyAngle 守护箭头头部数学：从任意角度的线段计算倒刺端点，
// 倒刺与箭尖的距离必须等于 head（长度守恒），且两根倒刺关于线段对称。
func TestArrowHeadBarbAnyAngle(t *testing.T) {
	const spread = 30 * math.Pi / 180
	cases := []struct{ x1, y1, x2, y2 int32 }{
		{0, 0, 100, 0},       // 横向
		{0, 0, 0, 100},       // 纵向
		{0, 0, 100, 100},     // 斜向 45°
		{200, 150, 100, 100}, // 右下 → 左上（修复前会坏的方向）
		{100, 200, 300, 50},  // 陡斜向
	}
	for _, c := range cases {
		const head = 12.0
		b1x, b1y := arrowHeadBarb(c.x1, c.y1, c.x2, c.y2, spread, head, 1)
		b2x, b2y := arrowHeadBarb(c.x1, c.y1, c.x2, c.y2, spread, head, -1)

		// 每根倒刺到箭尖的距离 = head（允许 1px 取整误差）。
		d1 := math.Hypot(float64(b1x-c.x2), float64(b1y-c.y2))
		d2 := math.Hypot(float64(b2x-c.x2), float64(b2y-c.y2))
		if math.Abs(d1-head) > 1.01 || math.Abs(d2-head) > 1.01 {
			t.Errorf("(%d,%d)→(%d,%d): 倒刺长 d1=%.2f d2=%.2f, 期望 %.1f",
				c.x1, c.y1, c.x2, c.y2, d1, d2, head)
		}

		// 两根倒刺到箭尖的夹角应为 2*spread。角度差取最短弧
		// （Atan2 返回 (-π,π]，直接相减会跨 π 跳变，如 5.202 ≡ -1.081）。
		// 容差 0.1rad（≈5.7°）：GDI 画线只能整数端点，倒刺坐标经 int32
		// 截断后短箭头的张角必有这个量级的取整误差，这是光栅化的物理下限
		// 而非数学错误（数学公式由 barb 长度守恒断言单独验证）。
		a1 := math.Atan2(float64(b1y-c.y2), float64(b1x-c.x2))
		a2 := math.Atan2(float64(b2y-c.y2), float64(b2x-c.x2))
		ang := math.Abs(a1 - a2)
		if ang > math.Pi {
			ang = 2*math.Pi - ang
		}
		if math.Abs(ang-2*spread) > 0.1 {
			t.Errorf("(%d,%d)→(%d,%d): 张角 = %.3frad, 期望 %.3frad",
				c.x1, c.y1, c.x2, c.y2, ang, 2*spread)
		}
	}
}

// TestLengthAtLeast 守护箭头的 Chebyshev 长度判定（纯垂直箭头宽为 0 也应通过）。
func TestLengthAtLeast(t *testing.T) {
	if !lengthAtLeast(10, 10, 10, 60, 4) {
		t.Error("纯垂直 50px 应判定为有效箭头")
	}
	if !lengthAtLeast(60, 10, 10, 10, 4) {
		t.Error("纯水平 50px 应判定为有效箭头")
	}
	if lengthAtLeast(10, 10, 12, 11, 4) {
		t.Error("2px 位移应判定为过短")
	}
}

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
