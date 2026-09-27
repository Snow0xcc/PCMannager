//go:build windows

package screenshot

import (
	"image"
	"path/filepath"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// The capture control bar is a small always-on-top window that stays visible
// while a recording or a scrolling capture runs.
//
// A separate window is required rather than a panel inside the editor: the
// editor covers the screen and would be captured into the result, so it is
// hidden for the duration, and a hidden window cannot host a stop button.
const (
	// ctrlW is sized so the widest state (three result buttons plus a status
	// readout) fits without the text running under the buttons.
	ctrlW    = 520
	ctrlH    = 52
	ctrlBtnW = 112
	ctrlBtnH = 30
	ctrlGap  = 6
	ctrlPad  = 10
)

// Control bar states.
const (
	ctrlRunning = iota
	ctrlResult
	// ctrlSaving is shown while the take is being encoded/written. Its only
	// meaningful content is the status text: offering buttons during an operation
	// that cannot be cancelled or repeated would just be a dead click.
	ctrlSaving
)

// Control bar messages.
const (
	ctrlWmLButtonUp = 0x0202
)

// ctrlButton is one control-bar action.
type ctrlButton struct {
	label string
	// act is dispatched by the control window's handler.
	act func()
}

// toggleRecording starts a GIF recording of the selection, or stops the running
// one and collects the result.
func (e *editorState) toggleRecording() {
	e.mu.Lock()
	running := e.capRunning
	sel := e.sel
	origin := e.origin
	e.mu.Unlock()

	if running {
		e.collectCapture(true)
		return
	}
	if e.hooks.StartRecord == nil {
		return
	}
	region := e.screenSelection(sel, origin)
	if region == nil {
		e.ctx.Bus.Notice(moduleID, "请先框选要录制的区域")
		return
	}
	e.parkEditor()
	if err := e.hooks.StartRecord(*region); err != nil {
		e.ctx.Logger.Warn("开始录屏失败", "module", moduleID, "err", err)
		e.ctx.Bus.Notice(moduleID, "开始录屏失败："+err.Error())
		e.unparkEditor()
		return
	}
	e.mu.Lock()
	e.capRunning = true
	e.mu.Unlock()
	e.ensureControlBar()
	e.repaint()
}

// startScroll begins a scrolling capture of the selection.
//
// The editor is closed rather than hidden: the user has to scroll the target
// window, and a full-screen popup that merely hides would still hold the
// keyboard focus that the target needs to receive the wheel.
func (e *editorState) startScroll() {
	e.mu.Lock()
	sel := e.sel
	origin := e.origin
	auto := e.scrollAuto
	e.mu.Unlock()

	if e.hooks.StartScroll == nil {
		return
	}
	region := e.screenSelection(sel, origin)
	if region == nil {
		e.ctx.Bus.Notice(moduleID, "请先框选需要滚动的区域")
		return
	}
	if err := e.hooks.StartScroll(*region, auto); err != nil {
		e.ctx.Logger.Warn("开始滚动截图失败", "module", moduleID, "err", err)
		e.ctx.Bus.Notice(moduleID, "开始滚动截图失败："+err.Error())
		return
	}
	e.mu.Lock()
	e.capRunning = true
	e.mu.Unlock()

	// Hide first (so the very first frame is not the overlay), then hand the
	// screen to the user.
	e.parkEditor()
	e.ensureControlBar()
}

// toggleScrollAuto flips the auto-scroll switch and repaints the toolbar.
func (e *editorState) toggleScrollAuto() {
	e.mu.Lock()
	e.scrollAuto = !e.scrollAuto
	e.mu.Unlock()
	e.repaint()
}

// screenSelection converts the current client-space selection into a screen
// rectangle, or nil when there is no usable selection.
func (e *editorState) screenSelection(sel winui.Rect, origin image.Point) *image.Rectangle {
	if sel.Width() < edMinSel || sel.Height() < edMinSel {
		return nil
	}
	out := image.Rect(int(sel.Left), int(sel.Top), int(sel.Right), int(sel.Bottom)).
		Add(origin)
	return &out
}

// parkEditor hides the editor so it is not captured into the result.
func (e *editorState) parkEditor() {
	e.mu.Lock()
	win := e.win
	e.mu.Unlock()
	if win != nil {
		win.Hide()
	}
}

// unparkEditor re-shows the editor after a failed start.
func (e *editorState) unparkEditor() {
	e.mu.Lock()
	win := e.win
	e.mu.Unlock()
	if win != nil {
		win.Show()
	}
}

// ensureControlBar creates (once) and shows the capture control window.
func (e *editorState) ensureControlBar() {
	e.mu.Lock()
	if e.ctrl != nil {
		ctrl := e.ctrl
		e.mu.Unlock()
		winui.ShowWindow(ctrl.HWND(), winui.SW_SHOW)
		winui.BringToTop(ctrl.HWND())
		return
	}
	e.mu.Unlock()

	// WS_EX_TOPMOST keeps the bar above the window being recorded (otherwise a
	// full-screen target would cover it and leave no way to stop), and
	// WS_EX_TOOLWINDOW keeps it out of the taskbar and Alt+Tab.
	w, err := winui.NewWindow("GoBoxCaptureCtrl", winui.WS_POPUP,
		winui.WS_EX_TOPMOST|winui.WS_EX_TOOLWINDOW, winui.Invalid)
	if err != nil {
		e.ctx.Logger.Error("创建录屏控制条失败", "module", moduleID, "err", err)
		e.ctx.Bus.Notice(moduleID, "无法创建控制条，可用 Esc 结束")
		return
	}
	w.Handle = e.ctrlProc

	sw, sh := winui.ScreenSize()
	x := sw - ctrlW - 24
	y := sh - ctrlH - 64
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if err := winui.SetWindowPos(w.HWND(), winui.Invalid, x, y, ctrlW, ctrlH,
		winui.SWP_NOZORDER|winui.SWP_SHOWWINDOW); err != nil {
		e.ctx.Logger.Warn("定位控制条失败", "module", moduleID, "err", err)
	}
	winui.SetTimer(w.HWND(), edCtrlTimer, edCtrlTimerM)

	e.mu.Lock()
	e.ctrl = w
	if e.ctrlFont == 0 {
		// A normal weight: the bar sits on an opaque surface, so ClearType is fine
		// here (unlike the taskbar widget, which needs hard-edged glyphs).
		e.ctrlFont = winui.NewFont("Microsoft YaHei", 12, winui.FW_NORMAL)
	}
	e.mu.Unlock()
	winui.SetForegroundWindow(w.HWND())
}

// ctrlProc dispatches the control bar's window messages.
func (e *editorState) ctrlProc(hwnd winui.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case winui.WM_PAINT:
		e.paintControl(hwnd)
		return 0, true
	case winui.WM_ERASEBKGND:
		return 1, true
	case winui.WM_TIMER:
		e.pollCapture()
		return 0, true
	case ctrlWmLButtonUp:
		e.onControlClick(int32(lParam&0xFFFF), int32(lParam>>16))
		return 0, true
	case edWmKeyDown:
		// Esc on the control bar keeps the result, matching the editor's own
		// shortcut so a running capture is never trapped.
		if int(wParam) == edVkEscape {
			e.collectCapture(true)
			return 0, true
		}
		return 0, false
	case winui.WM_CLOSE, winui.WM_DESTROY:
		// Closing the bar must not leave a capture running unattended.
		if e.isCapRunning() {
			e.collectCapture(true)
			return 0, true
		}
		return 0, false
	}
	return 0, false
}

// pollCapture notices a capture that ended on its own (frame cap reached, or the
// bottom of the page) and collects it, and keeps the bar repainted so the
// elapsed readout stays live.
func (e *editorState) pollCapture() {
	if !e.isCapRunning() {
		return
	}
	text, done := "", false
	if e.hooks.Status != nil {
		text, done = e.hooks.Status()
	}
	e.mu.Lock()
	e.ctrlStatus = text
	ctrl := e.ctrl
	e.mu.Unlock()

	if done {
		// The capture already stopped itself, so there is nothing to stop: only
		// the result needs collecting.
		e.collectCapture(true)
		return
	}
	if ctrl != nil {
		winui.InvalidateRect(ctrl.HWND())
	}
}

// isCapRunning reports whether a capture is in flight.
func (e *editorState) isCapRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.capRunning
}

// collectCapture ends the running capture and shows its result.
//
// keep=false abandons the capture: the module discards the take and the editor
// closes without saving anything.
//
// The stop + encode step runs on its own goroutine: finalising a recording means
// encoding every buffered frame into a GIF, which can take seconds, and doing
// that on the control window's message thread would freeze the very window the
// user is looking at (Windows would mark it "not responding"). The result is
// published back into the editor state and the window is invalidated, which is
// safe from any thread.
func (e *editorState) collectCapture(keep bool) {
	e.mu.Lock()
	if !e.capRunning || e.collected {
		e.mu.Unlock()
		return
	}
	e.collected = true
	e.capRunning = false
	e.mu.Unlock()

	if !keep {
		if e.hooks.Abort != nil {
			e.hooks.Abort()
		}
		e.closeEditorWindow()
		return
	}

	e.setCtrlStatus("正在保存…")
	e.mu.Lock()
	e.ctrlState = ctrlSaving
	e.mu.Unlock()

	go func() {
		text := ""
		if e.hooks.Stop != nil {
			text = e.hooks.Stop()
		}
		_, path := sharedOutcome.take()
		if text == "" {
			text = "已完成"
		}

		e.mu.Lock()
		e.ctrlState = ctrlResult
		e.ctrlStatus = text
		e.ctrlResult = path
		ctrl := e.ctrl
		e.mu.Unlock()

		if ctrl != nil {
			// InvalidateRect posts a paint, so this works across threads; the
			// foreground calls are best-effort (the user may have moved on) and are
			// what makes the result bar the place their eyes already are.
			winui.InvalidateRect(ctrl.HWND())
			winui.BringToTop(ctrl.HWND())
			winui.SetForegroundWindow(ctrl.HWND())
		}
	}()
}

// onControlClick dispatches a click on the control bar.
func (e *editorState) onControlClick(x, y int32) {
	btns := e.controlButtons()
	for i := range btns {
		r := e.ctrlButtonRect(i, len(btns))
		if x >= r.Left && x <= r.Right && y >= r.Top && y <= r.Bottom {
			btns[i].act()
			return
		}
	}
}

// controlButtons returns the actions for the bar's current state.
func (e *editorState) controlButtons() []ctrlButton {
	e.mu.Lock()
	state := e.ctrlState
	mode := e.mode
	hooks := e.hooks
	e.mu.Unlock()

	// While the take is being written there is nothing the user can usefully do:
	// the stop already happened, and a second one would be a dead click.
	if state == ctrlSaving {
		return nil
	}

	if state == ctrlRunning {
		if mode == edModeScroll {
			return []ctrlButton{
				{"停止并保存", func() { e.collectCapture(true) }},
				{"取消", func() { e.collectCapture(false) }},
			}
		}
		return []ctrlButton{
			{"停止并保存", func() { e.collectCapture(true) }},
			{"丢弃", func() { e.collectCapture(false) }},
		}
	}

	buttons := []ctrlButton{}
	if hooks.CopyResult != nil {
		buttons = append(buttons, ctrlButton{"复制到剪贴板", func() { e.copyResult() }})
	}
	buttons = append(buttons,
		ctrlButton{"打开目录", func() { e.revealResult() }},
		ctrlButton{"关闭", func() { e.closeEditorWindow() }},
	)
	return buttons
}

// ctrlButtonRect returns the rectangle of the i-th control button, laid out
// right to left so the first entry is where the cursor lands.
func (e *editorState) ctrlButtonRect(i, n int) winui.Rect {
	right := int32(ctrlW - ctrlPad - i*(ctrlBtnW+ctrlGap))
	top := int32((ctrlH - ctrlBtnH) / 2)
	return winui.Rect{Left: right - ctrlBtnW, Top: top, Right: right, Bottom: top + ctrlBtnH}
}

// ctrlTextRect returns the area left for the status readout.
//
// It is derived from the CURRENT button count rather than a fixed split: with
// three result buttons the status gets much less room than while saving (when
// there are none), and a fixed split would either clip the status or overlap the
// buttons.
func ctrlTextRect(n int) winui.Rect {
	buttonArea := int32(0)
	if n > 0 {
		buttonArea = int32(n)*(ctrlBtnW+ctrlGap) - ctrlGap
	}
	right := int32(ctrlW) - ctrlPad - buttonArea - ctrlGap
	if right < ctrlPad+40 {
		right = ctrlPad + 40
	}
	return winui.Rect{Left: ctrlPad, Top: 0, Right: right, Bottom: ctrlH}
}

// copyResult re-copies the produced file to the clipboard.
func (e *editorState) copyResult() {
	e.mu.Lock()
	path := e.ctrlResult
	hooks := e.hooks
	e.mu.Unlock()
	if path == "" {
		return
	}
	if hooks.CopyResult != nil {
		if err := hooks.CopyResult(path); err != nil {
			e.setCtrlStatus("复制失败：" + err.Error())
			return
		}
	}
	e.setCtrlStatus("已复制到剪贴板")
}

// revealResult opens the folder holding the produced file.
func (e *editorState) revealResult() {
	e.mu.Lock()
	path := e.ctrlResult
	hooks := e.hooks
	e.mu.Unlock()
	if path == "" {
		return
	}
	if hooks.Reveal != nil {
		hooks.Reveal(filepath.Dir(path))
	}
	e.setCtrlStatus("已打开目录：" + filepath.Dir(path))
}

// setCtrlStatus updates the bar's readout text.
func (e *editorState) setCtrlStatus(text string) {
	e.mu.Lock()
	e.ctrlStatus = text
	ctrl := e.ctrl
	e.mu.Unlock()
	if ctrl != nil {
		winui.InvalidateRect(ctrl.HWND())
	}
}

// paintControl renders the capture control bar.
func (e *editorState) paintControl(hwnd winui.HWND) {
	c, ps := winui.BeginPaint(hwnd)
	if c.DC() == 0 {
		return
	}
	defer winui.EndPaint(hwnd, ps)

	rect := winui.ClientRect(hwnd)
	c.Fill(rect, edColorSurface)

	e.mu.Lock()
	state := e.ctrlState
	status := e.ctrlStatus
	e.mu.Unlock()

	// A thin accent edge makes the bar readable over an arbitrary background,
	// including a white document.
	c.StrokeRect(rect, edColorEdge, 1)

	// Keep a live readout even if the poll has not run yet, so the bar never
	// looks frozen.
	if state == ctrlRunning && status == "" {
		status = "准备中…"
	}

	// Status text on the left, buttons on the right.
	btns := e.controlButtons()

	restore := c.SelectFont(e.ctrlFont)
	defer restore()

	textRect := ctrlTextRect(len(btns))
	c.DrawText(status, textRect, edColorText,
		winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_END_ELLIPSIS|winui.DT_NOPREFIX)

	for i := range btns {
		fill := uint32(edColorAccent)
		if state == ctrlResult {
			switch i {
			case 0:
				fill = edColorSel
			case 1:
				fill = edColorSurface
			}
		} else if i == 0 {
			// The primary "stop" action is highlighted so it is easy to find.
			fill = edColorSel
		} else {
			fill = edColorSurface
		}
		drawCtrlButton(c, e.ctrlButtonRect(i, len(btns)), btns[i].label, fill)
	}
}

// drawCtrlButton paints one control-bar button: plate plus text label.
//
// The capture control bar keeps TEXT buttons, unlike the editor toolbar's
// icons: its actions (停止并保存/丢弃/复制到剪贴板…) have no natural single
// glyph, and this bar has room for labels. It must not use drawToolbarButton,
// which renders a glyph by edBtn id and would draw a meaningless dot here.
func drawCtrlButton(c *winui.Canvas, r winui.Rect, label string, fill uint32) {
	c.Fill(r, fill)
	c.DrawText(label, r, winui.ContrastText(fill),
		winui.DT_CENTER|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
}

// The clipboard and file-manager helpers live behind winui/sysutil; nothing
// platform specific is needed here beyond what those already provide.
