//go:build windows

package screenshot

import (
	"image"
	"image/draw"
	"runtime"
	"sync"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/winui"
)

// editorState is the single active screenshot editor.
//
// Only one can be open at a time: a second hotkey press closes the first rather
// than stacking two full-screen windows on top of each other.
type editorState struct {
	ctx *core.Context

	// mode selects the workflow the toolbar drives (crop, record or scroll).
	mode edMode
	// hooks carries the callbacks for the non-capture workflows.
	hooks editorHooks

	mu  sync.Mutex
	win *winui.Window
	img *image.RGBA
	sel winui.Rect
	// origin is where the capture starts in SCREEN coordinates. The client-space
	// selection is relative to it, and a screen capture needs the sum.
	origin   image.Point
	dragging bool
	anchorX  int32
	anchorY  int32
	onSave   func(image.Image)
	onCopy   func(image.Image)
	closed   bool

	// ctrl is the small control bar shown while a recording or scrolling capture
	// is running, or while its result is being offered.
	ctrl *winui.Window
	// capRunning is true while a recording or scrolling capture is in flight.
	capRunning bool
	// collected makes finalising a capture a one-shot action: both the user's
	// stop and the poll's auto-detect can race to it.
	collected bool
	// ctrlState selects between the running and result layouts of the bar.
	ctrlState int
	// ctrlStatus is the bar's live text, written from both the editor and the
	// capture's poll.
	ctrlStatus string
	// ctrlResult is the path produced by a finished capture, so "copy" and "open
	// folder" have something to act on.
	ctrlResult string
	// ctrlFont is the control bar's text font, created with the bar and released
	// when it is destroyed (like the other windows' fonts).
	ctrlFont uintptr
	// scrollAuto mirrors the auto-scroll toggle in scroll mode.
	scrollAuto bool

	// annots is the ordered list of drawn shapes; undo pops the last one.
	annots []annot
	// tool is the currently selected annotation kind, or -1 for none.
	tool int
	// drawing is true while a shape drag is in progress, and curAnnot is the
	// shape being built (re-drawn live so the user sees it before releasing).
	drawing  bool
	curAnnot annot
	// strokeColor/strokeWidth are the toolbar's current style, applied to newly
	// started shapes.
	strokeColor uint32
	strokeWidth int32

	// toolbarDX/DY is the user's drag offset for the whole toolbar row. The row
	// defaults to snapping under the selection; the handle lets the user move it
	// anywhere (it keeps its position until the editor closes).
	toolbarDX int32
	toolbarDY int32
	// handleDrag is true while the user is dragging the grab handle, and the
	// anchor remembers where in the toolbar the grab started so the row follows
	// the cursor 1:1 instead of jumping.
	handleDrag           bool
	handleAnchorX, handleAnchorY int32
	// hoverID is the toolbar button (or edHandleID) currently under the cursor,
	// or -1; it drives both the hover highlight and the tooltip.
	hoverID int
}

// edMu guards the one active editor instance.
var (
	edMu     sync.Mutex
	edActive *editorState
)

// Editor colours (Win32 COLORREF, 0x00BBGGRR).
const (
	edColorDim      = 0x00606060
	edColorSel      = 0x00FFB44E // BGR of #4EB4FF
	edColorSurface  = 0x001A1A1A
	edColorText     = 0x00F0F0F0
	edColorAccent   = 0x00B85A2A
	edColorDanger   = 0x006060FF
	edColorEdge     = 0x00707070
	edColorDisabled = 0x00909090

	// edMaskAlpha dims the non-selected area without hiding the screen: the
	// capture underneath must stay readable while the user picks a rectangle.
	// 150/255 keeps the desktop legible while making the pending crop obvious.
	edMaskAlpha = 150
)

// Editor geometry, in device pixels.
const (
	edBtnW   = 44
	edBtnH   = 30
	edBtnGap = 4
	edMargin = 16
	// edMinSel is the smallest selection treated as a real crop (px).
	edMinSel = 4
	// edHandleW is the width of the drag handle that leads the toolbar. It is
	// narrower than a button: it only has to be a comfortable grab target.
	edHandleW = 22
	// edTooltipGap is how far above a button its tooltip floats.
	edTooltipGap = 6
)

// edHint is the prompt shown before anything is selected.
const edHint = "拖动鼠标框选区域 · 右键或 Esc 取消"

// Mode-specific prompts. They state what the region will be used for, which the
// generic hint cannot: the same drag means crop, record or scroll depending on
// the button the user pressed in the panel.
const (
	edHintRecord = "拖动框选要录制的区域 · 右键或 Esc 取消"
	edHintScroll = "拖动框选需要滚动的区域 · 右键或 Esc 取消"
)

// Win32 messages the editor handles that winui does not name.
const (
	edWmKeyDown     = 0x0100
	edWmLButtonDown = 0x0201
	edWmLButtonUp   = 0x0202
	edWmMouseMove   = 0x0200
	edWmRButtonUp   = 0x0205
)

// edTimer is the editor's periodic timer id. It refreshes the control bar's
// elapsed counter and, for a recording, notices when the frame cap was hit so
// the take is finalised instead of being left unfinishable.
const (
	edTimer      = 1
	edTimerMS    = 500
	edCtrlTimer  = 2
	edCtrlTimerM = 400
)

// Virtual-key codes bound to the editor's actions.
const (
	edVkReturn = 0x0D
	edVkEscape = 0x1B
	edVkC      = 0x43
	edVkZ      = 0x5A
)

// Button ids.
//
// The capture toolbar lays them out right to left in id order; the record and
// scroll toolbars pick a subset through editorState.buttons, so adding a mode is
// a new id plus an entry there rather than a new layout routine.
const (
	edBtnConfirm = iota
	edBtnCopy
	edBtnCancel
	edBtnUndo
	edBtnPen
	edBtnArrow
	edBtnEllipse
	edBtnRect

	// edBtnRecord starts (and, when pressed again, ends) a GIF recording.
	edBtnRecord
	// edBtnScrollStart begins a scrolling capture.
	edBtnScrollStart
	// edBtnScrollAuto toggles injected scrolling for a scrolling capture.
	edBtnScrollAuto
	// edBtnPin floats the selection on screen as a borderless topmost window.
	edBtnPin
)

// edHandleID is the pseudo button id returned when the drag handle is hit.
//
// It is deliberately not part of the edBtn* sequence: the handle is a toolbar
// affordance, not an action, and giving it a real id would let an accidental
// fall-through in activate() try to execute it.
const edHandleID = -2

// edButton is one entry of the current mode's toolbar.
//
// label is the tooltip text (and a debug aid); the toolbar itself renders a
// vector glyph instead of the label, so the buttons stay legible at any size.
type edButton struct {
	id    int
	label string
}

// buttons returns the toolbar for the editor's current mode.
//
// Index 0 is the RIGHTMOST button: buttonRect lays the row out right to left, so
// the primary action of every mode ends up where the cursor already is after a
// selection is dragged (bottom-right of the selection). The drag handle is not
// part of this list; buttonRect reserves its slot at the far left.
func (e *editorState) buttons() []edButton {
	switch e.mode {
	case edModeRecord:
		return []edButton{{edBtnRecord, "开始录制"}, {edBtnCancel, "取消"}}
	case edModeScroll:
		auto := "自动滚动：关"
		e.mu.Lock()
		on := e.scrollAuto
		e.mu.Unlock()
		if on {
			auto = "自动滚动：开"
		}
		return []edButton{
			{edBtnScrollStart, "开始滚动截图"},
			{edBtnScrollAuto, auto},
			{edBtnCancel, "取消"},
		}
	}
	return []edButton{
		{edBtnConfirm, "保存截图"},
		{edBtnCopy, "复制到剪贴板"},
		{edBtnPin, "屏幕贴图"},
		{edBtnCancel, "取消截图"},
		{edBtnUndo, "撤销编辑"},
		{edBtnPen, "画笔"},
		{edBtnArrow, "箭头"},
		{edBtnEllipse, "椭圆"},
		{edBtnRect, "矩形截图"},
	}
}

// edToolCount is the number of drawing tools in the toolbar.
const edToolCount = 4

// toolOf maps a toolbar button id to the annotation kind it selects, or -1 when
// the button is not a drawing tool.
func toolOf(btn int) int {
	switch btn {
	case edBtnRect:
		return int(annotRect)
	case edBtnEllipse:
		return int(annotEllipse)
	case edBtnArrow:
		return int(annotArrow)
	case edBtnPen:
		return int(annotPen)
	}
	return -1
}

// isToolButton reports whether a toolbar button selects a drawing tool.
func isToolButton(btn int) bool { return toolOf(btn) >= 0 }

// openEditor shows a full-screen window with the captured image and lets the
// user drag a region. What happens to that region (crop, record, scroll) is
// selected by mode and carried out through hooks, so all three workflows share
// one overlay implementation (Snipaste/Feishu style).
//
// It returns immediately: the editor owns a window and therefore its own
// thread, and blocking here would stall the hotkey dispatcher.
func openEditor(ctx *core.Context, img *image.RGBA, bounds image.Rectangle, mode edMode, hooks editorHooks) {
	if ctx == nil || img == nil {
		return
	}

	edMu.Lock()
	if prev := edActive; prev != nil && !prev.isClosed() {
		edMu.Unlock()
		prev.close()
		return
	}
	ed := &editorState{
		ctx:         ctx,
		mode:        mode,
		hooks:       hooks,
		img:         img,
		origin:      bounds.Min,
		onSave:      hooks.Save,
		onCopy:      hooks.Copy,
		tool:        -1,
		strokeColor: annotPalette[0],
		strokeWidth: annotWidths[1],
	}
	edActive = ed
	edMu.Unlock()

	go func() {
		// A window belongs to the thread that creates it and its messages are
		// only retrievable there, so this goroutine keeps its OS thread for the
		// editor's whole lifetime. It never unlocks: letting the goroutine end
		// while still locked would retire the thread and its message queue.
		runtime.LockOSThread()
		ed.run(bounds)
	}()
}

// run creates the editor window and pumps its messages until it closes.
func (e *editorState) run(bounds image.Rectangle) {
	winui.SetDPIAware()

	w, err := winui.NewWindow("GoBoxScreenshot", winui.WS_POPUP, 0, winui.Invalid)
	if err != nil {
		e.ctx.Logger.Error("创建截图窗口失败", "module", moduleID, "err", err)
		e.ctx.Bus.Log(moduleID, "error", "无法打开截图窗口")
		e.retire()
		return
	}
	w.Handle = e.wndProc

	e.mu.Lock()
	e.win = w
	e.mu.Unlock()

	if err := winui.SetWindowPos(w.HWND(), winui.Invalid,
		int32(bounds.Min.X), int32(bounds.Min.Y),
		int32(bounds.Dx()), int32(bounds.Dy()),
		winui.SWP_NOZORDER|winui.SWP_SHOWWINDOW); err != nil {
		e.ctx.Logger.Warn("调整截图窗口大小失败", "module", moduleID, "err", err)
	}
	w.Show()

	// A periodic timer drives both the hint repaint (not needed) and the
	// recording bookkeeping; it is installed on the editor window so it stops when
	// the window is destroyed.
	winui.SetTimer(w.HWND(), edTimer, edTimerMS)

	winui.MessageLoop(nil)
	e.teardown()
}

// teardown releases the window exactly once.
func (e *editorState) teardown() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	win := e.win
	ctrl := e.ctrl
	e.win = nil
	e.ctrl = nil
	e.mu.Unlock()

	// The control bar is a separate window on the same thread, so it must be
	// destroyed here (DestroyWindow from another thread silently fails and leaks
	// a dead window on screen).
	font := e.ctrlFont
	e.ctrlFont = 0
	if ctrl != nil {
		winui.KillTimer(ctrl.HWND(), edCtrlTimer)
		ctrl.Destroy()
	}
	if font != 0 {
		winui.DeleteObject(font)
	}
	if win != nil {
		winui.KillTimer(win.HWND(), edTimer)
		win.Destroy()
	}
	e.retire()
	winui.PostQuitMessage(0)
}

// retire forgets this editor so the next capture can open a fresh one.
func (e *editorState) retire() {
	edMu.Lock()
	if edActive == e {
		edActive = nil
	}
	edMu.Unlock()
}

// isClosed reports whether the editor has been torn down.
func (e *editorState) isClosed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closed
}

// close asks the editor window to shut down.
func (e *editorState) close() {
	e.mu.Lock()
	win := e.win
	e.mu.Unlock()
	if win != nil {
		winui.PostMessage(win.HWND(), winui.WM_CLOSE, 0, 0)
	}
}

// wndProc dispatches the editor's window messages.
func (e *editorState) wndProc(hwnd winui.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case winui.WM_PAINT:
		e.paint(hwnd)
		return 0, true
	case winui.WM_ERASEBKGND:
		return 1, true // the painter fills the background itself
	case edWmKeyDown:
		e.onKey(int(wParam))
		return 0, true
	case edWmLButtonDown:
		e.onDown(int32(lParam&0xFFFF), int32(lParam>>16))
		return 0, true
	case edWmMouseMove:
		e.onMove(int32(lParam&0xFFFF), int32(lParam>>16))
		return 0, true
	case edWmLButtonUp:
		e.onUp(int32(lParam&0xFFFF), int32(lParam>>16))
		return 0, true
	case winui.WM_TIMER:
		e.onTimer(uintptr(wParam))
		return 0, true
	case edWmRButtonUp:
		e.cancel()
		return 0, true
	case winui.WM_CLOSE, winui.WM_DESTROY:
		e.teardown()
		return 0, true
	}
	return 0, false
}

// onTimer refreshes the capture control bar.
func (e *editorState) onTimer(id uintptr) {
	if id != edTimer && id != edCtrlTimer {
		return
	}
	e.mu.Lock()
	running := e.capRunning
	ctrl := e.ctrl
	e.mu.Unlock()
	if !running || ctrl == nil {
		return
	}
	winui.InvalidateRect(ctrl.HWND())
}

// onKey maps the editor's keyboard shortcuts onto the toolbar actions.
//
// While a capture is running the only meaningful key is Esc, which stops the
// capture and keeps the result: the crop shortcuts would either save a region
// that is still being recorded or close the window out from under the sampler.
func (e *editorState) onKey(vk int) {
	if e.isCapRunning() {
		if vk == edVkEscape {
			e.collectCapture(true)
		}
		return
	}
	switch vk {
	case edVkReturn: // 确认
		e.save()
	case edVkC: // 复制
		e.copy()
	case edVkZ: // 撤销
		e.undo()
	case edVkEscape:
		e.cancel()
	}
}

// onDown starts either a new selection, a shape draw, or reports a button hit.
//
// Order matters: toolbar and style clicks first, then annotation drags (once a
// tool is active and a selection exists), and only then selection dragging, so
// drawing inside the selected area does not restart the crop.
func (e *editorState) onDown(x, y int32) {
	id := e.hitButton(x, y)
	if id == edHandleID {
		// Grabbing the handle starts a toolbar drag. The anchor is where inside
		// the ROW the grab happened, so the toolbar follows the cursor 1:1.
		e.mu.Lock()
		sel := e.sel
		e.handleDrag = true
		e.handleAnchorX = x
		e.handleAnchorY = y
		_ = sel
		e.mu.Unlock()
		return
	}
	if id >= 0 {
		e.activate(id)
		return
	}
	if id := e.hitStyle(x, y); id >= 0 {
		e.applyStyle(id)
		return
	}
	if e.beginDraw(x, y) {
		return
	}
	e.mu.Lock()
	e.dragging = true
	e.anchorX, e.anchorY = x, y
	e.sel = winui.Rect{Left: x, Top: y, Right: x, Bottom: y}
	e.mu.Unlock()
	e.repaint()
}

// beginDraw starts an annotation if a tool is selected and a crop exists.
// Drawing is confined to the selection (that is the region being captured), and
// it only begins inside that selection.
func (e *editorState) beginDraw(x, y int32) bool {
	e.mu.Lock()
	tool := e.tool
	sel := e.sel
	color := e.strokeColor
	width := e.strokeWidth
	mode := e.mode
	e.mu.Unlock()

	// Annotations belong to the crop workflow: drawing on a region that is about
	// to be recorded or scrolled into a long shot would bake shapes into the
	// capture, and neither workflow has an undo for that.
	if mode != edModeCapture {
		return false
	}
	if tool < 0 {
		return false
	}
	if sel.Width() < edMinSel || sel.Height() < edMinSel {
		return false
	}
	if !pointInRect(x, y, sel) {
		return false
	}

	kind := annotKind(tool)
	a := annot{Kind: kind, Color: color, Width: width}
	if kind == annotPen {
		a.Points = newPenStroke(x, y)
	} else {
		a.From = winui.Rect{Left: x, Top: y, Right: x, Bottom: y}
		a.To = winui.Rect{Left: x, Top: y, Right: x, Bottom: y}
	}

	e.mu.Lock()
	e.drawing = true
	e.curAnnot = a
	e.mu.Unlock()
	e.repaint()
	return true
}

// activate runs the action bound to a toolbar button.
func (e *editorState) activate(id int) {
	switch id {
	case edBtnRecord:
		e.toggleRecording()
		return
	case edBtnScrollStart:
		e.startScroll()
		return
	case edBtnScrollAuto:
		e.toggleScrollAuto()
		return
	}

	if tool := toolOf(id); tool >= 0 {
		e.mu.Lock()
		// Toggling the active tool off returns to plain selection/crop mode.
		if e.tool == tool {
			e.tool = -1
		} else {
			e.tool = tool
		}
		e.mu.Unlock()
		e.repaint()
		return
	}

	switch id {
	case edBtnConfirm:
		e.save()
	case edBtnCopy:
		e.copy()
	case edBtnPin:
		e.pinToScreen()
	case edBtnCancel:
		e.cancel()
	case edBtnUndo:
		e.undo()
	}
}

// pinToScreen floats the current selection (with its annotations) as a
// borderless topmost window, then closes the editor.
//
// Closing is deliberate: the user has already re-used the capture by pinning
// it, and leaving a full-screen dimming overlay up would invite them to pin the
// same region twice. The pin keeps living in its own window/thread.
func (e *editorState) pinToScreen() {
	img := e.export()
	if img == nil {
		return
	}
	e.mu.Lock()
	sel := e.sel
	origin := e.origin
	e.mu.Unlock()

	pos := image.Pt(origin.X+int(sel.Left), origin.Y+int(sel.Top))
	if err := pinImage(e.ctx, img, pos); err != nil {
		e.ctx.Logger.Warn("贴图失败", "module", moduleID, "err", err)
		e.ctx.Bus.Notice(moduleID, "贴图失败："+err.Error())
		return
	}
	e.close()
}

// closeEditorWindow asks the editor's own window to close, from any goroutine.
//
// The control bar's buttons run on the control window's thread; posting the
// close rather than calling teardown directly keeps every window operation on
// its owning thread, which is the rule that DestroyWindow depends on.
func (e *editorState) closeEditorWindow() {
	e.mu.Lock()
	win := e.win
	e.mu.Unlock()
	if win != nil {
		winui.PostMessage(win.HWND(), winui.WM_CLOSE, 0, 0)
	}
}

// undo removes the most recent annotation. It is a no-op when there are none.
func (e *editorState) undo() {
	e.mu.Lock()
	if n := len(e.annots); n > 0 {
		e.annots = e.annots[:n-1]
	}
	e.mu.Unlock()
	e.repaint()
}

// hitButton reports which toolbar button contains (x,y), or -1 for none.
// The toolbar only exists once a selection has been made, so there is nothing
// to hit before that. The drag handle reports edHandleID.
func (e *editorState) hitButton(x, y int32) int {
	e.mu.Lock()
	sel := e.sel
	e.mu.Unlock()
	if sel.Width() < edMinSel || sel.Height() < edMinSel {
		return -1
	}
	if h := e.handleRect(sel); x >= h.Left && x <= h.Right && y >= h.Top && y <= h.Bottom {
		return edHandleID
	}
	btns := e.buttons()
	for i := range btns {
		r := e.buttonRect(i, sel)
		if x >= r.Left && x <= r.Right && y >= r.Top && y <= r.Bottom {
			return btns[i].id
		}
	}
	return -1
}

// hitStyle reports which style control contains (x,y): a palette index (>=0),
// a width index (>=100), or -1 for none.
//
// The offset of 100 separates the two id spaces so callers can switch on the
// result without a second return value.
func (e *editorState) hitStyle(x, y int32) int {
	e.mu.Lock()
	sel := e.sel
	e.mu.Unlock()
	if sel.Width() < edMinSel || sel.Height() < edMinSel {
		return -1
	}
	full := winui.ClientRect(e.hwndForPaint())
	if full.Width() <= 0 {
		return -1
	}
	bar := e.styleBarRect(sel, full)
	if y < bar.Top || y > bar.Bottom {
		return -1
	}
	for i := range annotPalette {
		r := e.swatchRect(i, bar)
		if x >= r.Left && x <= r.Right {
			return i
		}
	}
	for i := range annotWidths {
		r := e.widthDotRect(i, bar)
		if x >= r.Left && x <= r.Right {
			return 100 + i
		}
	}
	return -1
}

// applyStyle selects a colour or width from a hitStyle result.
func (e *editorState) applyStyle(id int) {
	switch {
	case id >= 100 && id-100 < len(annotWidths):
		e.mu.Lock()
		e.strokeWidth = annotWidths[id-100]
		e.mu.Unlock()
	case id >= 0 && id < len(annotPalette):
		e.mu.Lock()
		e.strokeColor = annotPalette[id]
		e.mu.Unlock()
	}
	e.repaint()
}

// edToolbarWidth returns the toolbar row's full width for n buttons: the drag
// handle at the far left, the gap after it, then the buttons right to left.
func edToolbarWidth(n int) int32 {
	return edHandleW + edBtnGap + int32(n)*(edBtnW+edBtnGap) - edBtnGap
}

// buttonRect returns the rectangle of the i-th toolbar button.
//
// The toolbar hangs just below the selection (Feishu/Snipaste style) so the
// confirm/cancel actions are next to what they apply to, rather than in a bar
// far away at the screen edge. It flips above the selection when there is no
// room below, is clamped inside the window horizontally, and is then shifted by
// the user's drag offset (toolbarDX/DY) — clamped again so the row can always
// be reached, wherever it was dragged.
func (e *editorState) buttonRect(i int, sel winui.Rect) winui.Rect {
	e.mu.Lock()
	dx, dy := e.toolbarDX, e.toolbarDY
	e.mu.Unlock()
	win := e.win
	full := winui.ClientRect(e.hwndLocked(win))

	// The width follows the CURRENT mode's button count, so a two-button record
	// toolbar does not leave a gap where six more buttons used to be.
	n := len(e.buttons())
	toolbarW := edToolbarWidth(n)
	top := sel.Bottom + edBtnGap
	if top+edBtnH > full.Bottom {
		// No room below: place it above the selection instead.
		top = sel.Top - edBtnGap - edBtnH
	}
	if top < full.Top {
		top = full.Top
	}
	left := sel.Right - toolbarW
	if max := full.Right - edMargin - toolbarW; left > max {
		left = max
	}
	if left < full.Left+edMargin {
		left = full.Left + edMargin
	}

	// Apply the drag offset, keeping the row fully inside the window: a toolbar
	// dragged half out of view would have unreachable buttons. The window must
	// be at least as wide/tall as the toolbar for these bounds to be sane; a
	// zero rect (no window yet) or a degenerate one leaves the position alone
	// rather than clamping everything into a negative coordinate.
	left += dx
	top += dy
	if full.Right-full.Left >= toolbarW && full.Bottom-full.Top >= edBtnH {
		if left < full.Left {
			left = full.Left
		}
		if max := full.Right - toolbarW; left > max {
			left = max
		}
		if top < full.Top {
			top = full.Top
		}
		if max := full.Bottom - edBtnH; top > max {
			top = max
		}
	}

	// Layout is right to left: button 0 is the rightmost, and the handle sits
	// at the far left of the row (its own slot, not part of the button list).
	right := left + toolbarW - edHandleW - edBtnGap - int32(i)*(edBtnW+edBtnGap)
	return winui.Rect{Left: right - edBtnW, Top: top, Right: right, Bottom: top + edBtnH}
}

// handleRect returns the drag-handle rectangle: the leftmost slot of the row.
// It shares buttonRect's positioning logic so the two can never disagree about
// where the row is (that mismatch would make clicks land off target).
func (e *editorState) handleRect(sel winui.Rect) winui.Rect {
	b0 := e.buttonRect(len(e.buttons())-1, sel)
	left := b0.Left - edBtnGap - edHandleW
	return winui.Rect{Left: left, Top: b0.Top, Right: left + edHandleW, Bottom: b0.Bottom}
}

// hwndLocked reads the window handle without holding the lock during the call.
func (e *editorState) hwndLocked(win *winui.Window) winui.HWND {
	if win == nil {
		return winui.Invalid
	}
	return win.HWND()
}

// onMove extends the selection or the in-progress annotation, drags the
// toolbar by its handle, or updates the hover state for tooltips.
func (e *editorState) onMove(x, y int32) {
	e.mu.Lock()
	if e.handleDrag {
		// Move the toolbar by the distance the cursor travelled since the grab;
		// buttonRect clamps the result back inside the window.
		e.toolbarDX += x - e.handleAnchorX
		e.toolbarDY += y - e.handleAnchorY
		e.handleAnchorX, e.handleAnchorY = x, y
		e.mu.Unlock()
		e.repaint()
		return
	}
	if e.drawing {
		e.growAnnotLocked(x, y)
		e.mu.Unlock()
		e.repaint()
		return
	}
	if !e.dragging {
		// Not dragging anything: update the hover highlight / tooltip.
		hover := e.hitButton(x, y)
		changed := hover != e.hoverID
		e.hoverID = hover
		e.mu.Unlock()
		if changed {
			e.repaint()
		}
		return
	}
	e.sel = normalize(winui.Rect{Left: e.anchorX, Top: e.anchorY, Right: x, Bottom: y})
	e.mu.Unlock()
	e.repaint()
}

// growAnnotLocked extends the current annotation to (x,y). Caller holds mu.
func (e *editorState) growAnnotLocked(x, y int32) {
	a := e.curAnnot
	switch a.Kind {
	case annotPen:
		a.Points = appendPenPoint(a.Points, x, y)
	default:
		a.To = winui.Rect{Left: x, Top: y, Right: x, Bottom: y}
	}
	e.curAnnot = a
}

// onUp finishes the drag, dropping taps too small to be a real selection or
// shape.
func (e *editorState) onUp(x, y int32) {
	e.mu.Lock()
	if e.handleDrag {
		e.handleDrag = false
		e.mu.Unlock()
		return
	}
	if e.drawing {
		e.growAnnotLocked(x, y)
		a := e.curAnnot
		e.drawing = false
		e.curAnnot = annot{}
		// Keep only shapes that actually drew something.
		if a.Kind == annotPen {
			if len(a.Points) >= 2 {
				e.annots = append(e.annots, a)
			}
		} else {
			r := normalizePointRect(a.From.Left, a.From.Top, a.To.Left, a.To.Top)
			if r.Width() >= edMinSel || r.Height() >= edMinSel {
				a.From = r
				a.To = winui.Rect{Left: a.To.Left, Top: a.To.Top}
				e.annots = append(e.annots, a)
			}
		}
		e.mu.Unlock()
		e.repaint()
		return
	}

	if !e.dragging {
		e.mu.Unlock()
		return
	}
	e.dragging = false
	r := normalize(winui.Rect{Left: e.anchorX, Top: e.anchorY, Right: x, Bottom: y})
	if r.Width() < edMinSel || r.Height() < edMinSel {
		r = winui.Rect{}
	}
	e.sel = r
	e.mu.Unlock()
	e.repaint()
}

// endHandleDrag finishes a toolbar drag started on the grab handle.
func (e *editorState) endHandleDrag() {
	e.mu.Lock()
	e.handleDrag = false
	e.mu.Unlock()
}

// pointInRect reports whether (x,y) lies inside r, inclusive.
func pointInRect(x, y int32, r winui.Rect) bool {
	return x >= r.Left && x <= r.Right && y >= r.Top && y <= r.Bottom
}

// save crops the selection and hands it to the module.
func (e *editorState) save() {
	if img := e.export(); img != nil && e.onSave != nil {
		e.onSave(img)
	}
	e.close()
}

// copy crops the selection and pushes it onto the clipboard.
func (e *editorState) copy() {
	if img := e.export(); img != nil && e.onCopy != nil {
		e.onCopy(img)
	}
	e.close()
}

// export returns the cropped selection with every annotation baked in, or nil
// when there is no usable selection.
//
// Coordinates: the editor window is a borderless popup placed exactly over the
// captured rectangle, so a mouse position in client space is already a position
// in the capture's image space (both start at the window's top-left). Converting
// an annotation to the cropped image therefore only needs the crop origin
// subtracted.
func (e *editorState) export() image.Image {
	r := e.selection()
	if r == nil {
		return nil
	}

	e.mu.Lock()
	base := e.img
	annots := append([]annot(nil), e.annots...)
	e.mu.Unlock()
	if base == nil {
		return nil
	}

	cropped := cropImage(base, *r)
	if len(annots) == 0 {
		return cropped
	}

	shifted := make([]annot, len(annots))
	for i, a := range annots {
		shifted[i] = a.translated(-int32(r.Min.X), -int32(r.Min.Y))
	}

	out := winui.RenderOverlay(cropped, func(c *winui.Canvas) {
		replay(c, shifted)
	})
	if out == nil {
		return cropped
	}
	return out
}

// cancel closes the editor without producing an image.
func (e *editorState) cancel() {
	e.ctx.Bus.Progress(moduleID, actionCapture, 0, "已取消截图")
	e.close()
}

// selection returns the drag rectangle, or nil when there is none.
func (e *editorState) selection() *image.Rectangle {
	e.mu.Lock()
	r := e.sel
	e.mu.Unlock()
	if r.Width() < edMinSel || r.Height() < edMinSel {
		return nil
	}
	b := e.img.Bounds()
	out := image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom))
	if out.Min.X < b.Min.X {
		out.Min.X = b.Min.X
	}
	if out.Min.Y < b.Min.Y {
		out.Min.Y = b.Min.Y
	}
	if out.Max.X > b.Max.X {
		out.Max.X = b.Max.X
	}
	if out.Max.Y > b.Max.Y {
		out.Max.Y = b.Max.Y
	}
	if out.Dx() < 1 || out.Dy() < 1 {
		return nil
	}
	return &out
}

// repaint schedules a redraw of the editor window.
func (e *editorState) repaint() {
	e.mu.Lock()
	win := e.win
	e.mu.Unlock()
	if win != nil {
		winui.InvalidateRect(win.HWND())
	}
}

// paint renders the screenshot, the dim mask, the selection toolbar and the
// selection size readout.
func (e *editorState) paint(hwnd winui.HWND) {
	c, ps := winui.BeginPaint(hwnd)
	if c.DC() == 0 {
		return
	}
	defer winui.EndPaint(hwnd, ps)

	full := winui.ClientRect(hwnd)

	e.mu.Lock()
	img := e.img
	sel := e.sel
	annots := append([]annot(nil), e.annots...)
	cur := e.curAnnot
	drawing := e.drawing
	tool := e.tool
	strokeColor := e.strokeColor
	strokeWidth := e.strokeWidth
	mode := e.mode
	e.mu.Unlock()

	// 1. The screenshot itself, filling the whole window. This is what makes the
	//    editor show "the page you are about to crop" instead of a blank frame.
	if img != nil {
		c.Image(full, img)
	}

	ready := sel.Width() >= edMinSel && sel.Height() >= edMinSel

	// 2. Dim outside the selection with a translucent wash rather than an opaque
	//    fill: the user must still see the screen underneath while choosing the
	//    crop rectangle. Inside the selection the image is redrawn at full
	//    brightness, which creates the usual "spotlight" effect.
	if ready {
		mask := clampRect(sel, full)
		c.FillAlpha(winui.Rect{Left: full.Left, Top: full.Top, Right: full.Right, Bottom: mask.Top}, edColorDim, edMaskAlpha)
		c.FillAlpha(winui.Rect{Left: full.Left, Top: mask.Bottom, Right: full.Right, Bottom: full.Bottom}, edColorDim, edMaskAlpha)
		c.FillAlpha(winui.Rect{Left: full.Left, Top: mask.Top, Right: mask.Left, Bottom: mask.Bottom}, edColorDim, edMaskAlpha)
		c.FillAlpha(winui.Rect{Left: mask.Right, Top: mask.Top, Right: full.Right, Bottom: mask.Bottom}, edColorDim, edMaskAlpha)

		// Redraw the selection from the capture at full brightness.
		if img != nil {
			src := winui.Rect{
				Left:   mask.Left - full.Left,
				Top:    mask.Top - full.Top,
				Right:  mask.Right - full.Left,
				Bottom: mask.Bottom - full.Top,
			}
			c.ImageSubRect(mask, src, img)
		}
		c.StrokeRect(mask, edColorSel, 1)

		// 2b. Replay the annotations, clipped to the selection so a shape that
		//     runs past the crop does not appear outside the area being captured.
		replay(c, annots)
		if drawing {
			cur.draw(c)
		}

		// 3. Selection toolbar, hanging just below the selection. Its contents
		//    depend on the mode: crop offers confirm/copy/undo plus the drawing
		//    tools, while record and scroll offer only their own start action and
		//    cancel. Buttons render vector glyphs (icons_windows.go), not text —
		//    the label is the tooltip — and the row starts with a drag handle.
		btns := e.buttons()
		hover := e.hoverIDLocked()
		for i, b := range btns {
			fill := uint32(edColorAccent)
			switch b.id {
			case edBtnCancel:
				fill = edColorDanger
			case edBtnUndo:
				fill = edColorSurface
			}
			// Highlight the active drawing tool so the current mode is obvious,
			// and the hovered button so the tooltip's subject is unambiguous.
			if isToolButton(b.id) && toolOf(b.id) == tool {
				fill = edColorSel
			}
			if hover == b.id {
				fill = winui.BlendColors(fill, edColorText, 0.25)
			}
			r := e.buttonRect(i, sel)
			drawToolbarButton(c, r, b.id, fill)
			if hover == b.id {
				e.paintTooltip(c, r, b.label)
			}
		}

		// The drag handle at the far left of the row; brighter while grabbed so
		// the user can see the toolbar is being carried.
		hr := e.handleRect(sel)
		handleFg := uint32(edColorText)
		if hover == edHandleID || e.handleDragLocked() {
			handleFg = edColorSel
		}
		c.Fill(hr, edColorSurface)
		drawDragHandle(c, hr, handleFg)
		if hover == edHandleID {
			e.paintTooltip(c, hr, "拖动工具栏")
		}

		// 4. Size readout above the selection, so the crop size is visible.
		e.paintSize(c, sel)

		// 5. Style bar for the drawing tools: swatches for colour, dots for width.
		//    Only the crop workflow has drawable annotations, so it is hidden
		//    otherwise rather than offering controls that do nothing.
		e.paintStyleBar(c, sel, strokeColor, strokeWidth, mode == edModeCapture && tool >= 0)
	} else {
		// Nothing selected yet: a light wash keeps the desktop readable while
		// making the mode obvious.
		c.FillAlpha(full, edColorDim, edMaskAlpha)
		c.DrawText(e.hintFor(mode), full, edColorText,
			winui.DT_CENTER|winui.DT_VCENTER|winui.DT_SINGLELINE)
	}
}

// hintFor returns the prompt for a mode.
func (e *editorState) hintFor(mode edMode) string {
	switch mode {
	case edModeRecord:
		return edHintRecord
	case edModeScroll:
		return edHintScroll
	}
	return edHint
}

// clampRect confines r to within bounds.
func clampRect(r, bounds winui.Rect) winui.Rect {
	if r.Left < bounds.Left {
		r.Left = bounds.Left
	}
	if r.Top < bounds.Top {
		r.Top = bounds.Top
	}
	if r.Right > bounds.Right {
		r.Right = bounds.Right
	}
	if r.Bottom > bounds.Bottom {
		r.Bottom = bounds.Bottom
	}
	return r
}

// paintSize draws the "W x H" readout above the selection.
func (e *editorState) paintSize(c *winui.Canvas, sel winui.Rect) {
	label := " " + itoa32(sel.Width()) + " x " + itoa32(sel.Height()) + " "
	w, h := c.MeasureText(label)

	top := sel.Top - h - 4
	if top < 0 {
		top = sel.Top
	}
	left := sel.Left
	if left+w > sel.Right {
		left = sel.Right - w
	}
	if left < 0 {
		left = 0
	}

	r := winui.Rect{Left: left, Top: top, Right: left + w, Bottom: top + h}
	c.Fill(r, edColorSurface)
	c.DrawText(label, r, edColorText,
		winui.DT_CENTER|winui.DT_VCENTER|winui.DT_SINGLELINE)
}

// itoa32 renders a non-negative int32 without importing strconv here.
func itoa32(n int32) string {
	if n <= 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 && i > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// paintStyleBar draws the colour swatches and width dots for the drawing tools.
//
// It sits on the row above the toolbar so the tool buttons and their style
// controls read as two connected groups, matching the reference behaviour
// (lines-thickness on the left, colour on the right as separate clusters).
// When no tool is active the bar is hidden: it would have nothing to style.
func (e *editorState) paintStyleBar(c *winui.Canvas, sel winui.Rect, color uint32, width int32, visible bool) {
	if !visible {
		return
	}
	full := winui.ClientRect(e.hwndForPaint())

	bar := e.styleBarRect(sel, full)
	c.Fill(bar, edColorSurface)

	// Colour swatches.
	for i := range annotPalette {
		sw := e.swatchRect(i, bar)
		c.Fill(sw, annotPalette[i])
		if annotPalette[i] == color {
			// A contrasting outline marks the current choice.
			c.StrokeRect(sw, edColorSel, 2)
		} else {
			c.StrokeRect(sw, edColorEdge, 1)
		}
	}

	// Width dots, right of the swatches.
	for i, wdt := range annotWidths {
		dr := e.widthDotRect(i, bar)
		cx := (dr.Left + dr.Right) / 2
		cy := (dr.Top + dr.Bottom) / 2
		r := wdt
		box := winui.Rect{Left: cx - r, Top: cy - r, Right: cx + r, Bottom: cy + r}
		if wdt == width {
			c.Fill(box, edColorSel)
		} else {
			c.Fill(box, edColorText)
		}
	}
}

// styleBarRect returns the row above the toolbar that holds the style controls.
func (e *editorState) styleBarRect(sel winui.Rect, full winui.Rect) winui.Rect {
	toolbar := e.toolbarRect(sel, full)
	top := toolbar.Top - edBtnH - edBtnGap
	if top < full.Top {
		top = toolbar.Bottom + edBtnGap
	}
	width := int32(len(annotPalette))*(annotSwatchW+annotSwatchGap) + int32(len(annotWidths))*30 + annotBarPad*2
	return winui.Rect{Left: toolbar.Left, Top: top, Right: toolbar.Left + width, Bottom: top + edBtnH}
}

// swatchRect returns the rectangle of the i-th colour swatch.
func (e *editorState) swatchRect(i int, bar winui.Rect) winui.Rect {
	left := bar.Left + annotBarPad + int32(i)*(annotSwatchW+annotSwatchGap)
	sz := int32(edBtnH - 8)
	top := bar.Top + (bar.Height()-sz)/2
	return winui.Rect{Left: left, Top: top, Right: left + sz, Bottom: top + sz}
}

// widthDotRect returns the rectangle of the i-th width control.
func (e *editorState) widthDotRect(i int, bar winui.Rect) winui.Rect {
	base := bar.Left + annotBarPad + int32(len(annotPalette))*(annotSwatchW+annotSwatchGap) + 8
	left := base + int32(i)*30
	return winui.Rect{Left: left, Top: bar.Top, Right: left + 28, Bottom: bar.Bottom}
}

// hwndForPaint returns the editor window handle for geometry queries.
func (e *editorState) hwndForPaint() winui.HWND {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.win == nil {
		return winui.Invalid
	}
	return e.win.HWND()
}

// toolbarRect is the bounding box of the whole button row; buttonRect positions
// individual buttons inside it.
func (e *editorState) toolbarRect(sel winui.Rect, full winui.Rect) winui.Rect {
	btns := e.buttons()
	first := e.buttonRect(0, sel)
	last := e.buttonRect(len(btns)-1, sel)
	return winui.Rect{Left: last.Left - edHandleW - edBtnGap, Top: first.Top, Right: first.Right, Bottom: first.Bottom}
}

// drawToolbarButton paints one editor-toolbar button: the plate plus its
// vector glyph.
//
// The label is NOT drawn — buttons are identified by icon (icons_windows.go) —
// so the glyph colour must contrast with the plate, whatever fill the caller
// picked (the palette varies: accent, danger, selection blue, surface). The
// tooltip carries the text description instead.
//
// The capture control bar (editor_control.go) keeps TEXT buttons: its actions
// (停止并保存/丢弃/复制到剪贴板…) have no natural single glyph, and that bar has
// room for labels.
func drawToolbarButton(c *winui.Canvas, r winui.Rect, id int, fill uint32) {
	c.Fill(r, fill)
	drawToolbarIcon(c, r, id, winui.ContrastText(fill))
}

// hoverIDLocked returns the hovered button id; the zero default (-1) means none.
func (e *editorState) hoverIDLocked() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hoverID
}

// handleDragLocked reports whether a toolbar drag is in progress.
func (e *editorState) handleDragLocked() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.handleDrag
}

// paintTooltip floats a small label above a button, describing it in Chinese.
//
// It is drawn as part of the editor's own paint pass (rather than via the
// common-controls TOOLTIPS_CLASS): the editor is a single window with full
// control of its paint order, so a hand-drawn label needs no extra child
// window, no message forwarding and no dependency on a second HWND — and it
// naturally sits above everything else because it is drawn last.
func (e *editorState) paintTooltip(c *winui.Canvas, btn winui.Rect, text string) {
	if text == "" {
		return
	}
	tw, th := c.MeasureText(" " + text + " ")
	if tw <= 0 || th <= 0 {
		return
	}

	// Centre on the button, then clamp inside the window so a tooltip near an
	// edge stays readable instead of being cut off.
	full := winui.ClientRect(e.hwndForPaint())
	left := btn.Left + (btn.Width()-tw)/2
	top := btn.Top - edTooltipGap - th
	if top < full.Top {
		// No room above (toolbar flipped to the top edge): show below instead.
		top = btn.Bottom + edTooltipGap
	}
	if left < full.Left {
		left = full.Left
	}
	if max := full.Right - tw; left > max {
		left = max
	}
	if top+th > full.Bottom {
		top = full.Bottom - th
	}

	r := winui.Rect{Left: left, Top: top, Right: left + tw, Bottom: top + th}
	c.Fill(r, edColorSurface)
	c.StrokeRect(r, edColorEdge, 1)
	c.DrawText(" "+text+" ", r, edColorText,
		winui.DT_CENTER|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
}

// normalize orders a rectangle so Left<=Right and Top<=Bottom.
func normalize(r winui.Rect) winui.Rect {
	if r.Left > r.Right {
		r.Left, r.Right = r.Right, r.Left
	}
	if r.Top > r.Bottom {
		r.Top, r.Bottom = r.Bottom, r.Top
	}
	return r
}

// cropImage returns a copy of the selected region.
func cropImage(img *image.RGBA, r image.Rectangle) image.Image {
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), img, r.Min, draw.Src)
	return out
}
