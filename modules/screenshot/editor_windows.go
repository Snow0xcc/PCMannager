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

	mu       sync.Mutex
	win      *winui.Window
	img      *image.RGBA
	sel      winui.Rect
	dragging bool
	anchorX  int32
	anchorY  int32
	onSave   func(image.Image)
	onCopy   func(image.Image)
	closed   bool
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
)

// Editor geometry, in device pixels.
const (
	edBarH   = 40
	edBtnW   = 96
	edBtnH   = 24
	edBtnGap = 8
	edMargin = 16
	// edMinSel is the smallest selection treated as a real crop (px).
	edMinSel = 4
)

// Win32 messages the editor handles that winui does not name.
const (
	edWmKeyDown     = 0x0100
	edWmLButtonDown = 0x0201
	edWmLButtonUp   = 0x0202
	edWmMouseMove   = 0x0200
	edWmRButtonUp   = 0x0205
)

// Virtual-key codes bound to the editor's actions.
const (
	edVkReturn = 0x0D
	edVkEscape = 0x1B
	edVkC      = 0x43
	edVkS      = 0x53
)

// Button ids, laid out right to left along the bottom bar.
const (
	edBtnSave = iota
	edBtnCopy
	edBtnCancel
)

// edButtonCount is how many buttons the bar holds.
const edButtonCount = 3

// openEditor shows a full-screen window with the captured image and lets the
// user drag a region, then Save / Copy / Cancel (Snipaste-style).
//
// It returns immediately: the editor owns a window and therefore its own
// thread, and blocking here would stall the hotkey dispatcher.
func openEditor(ctx *core.Context, img *image.RGBA, bounds image.Rectangle, onSave, onCopy func(image.Image)) {
	if ctx == nil || img == nil {
		return
	}

	edMu.Lock()
	if prev := edActive; prev != nil && !prev.isClosed() {
		edMu.Unlock()
		prev.close()
		return
	}
	ed := &editorState{ctx: ctx, img: img, onSave: onSave, onCopy: onCopy}
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
	e.win = nil
	e.mu.Unlock()

	if win != nil {
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
	case edWmRButtonUp:
		e.cancel()
		return 0, true
	case winui.WM_CLOSE, winui.WM_DESTROY:
		e.teardown()
		return 0, true
	}
	return 0, false
}

// onKey maps the editor's keyboard shortcuts onto the bottom-bar buttons.
func (e *editorState) onKey(vk int) {
	switch vk {
	case edVkReturn, edVkS:
		e.save()
	case edVkC:
		e.copy()
	case edVkEscape:
		e.cancel()
	}
}

// onDown starts a new selection at (x,y).
func (e *editorState) onDown(x, y int32) {
	e.mu.Lock()
	e.dragging = true
	e.anchorX, e.anchorY = x, y
	e.sel = winui.Rect{Left: x, Top: y, Right: x, Bottom: y}
	e.mu.Unlock()
	e.repaint()
}

// hitButton reports which bottom-bar button contains (x,y), or -1 for none.
func (e *editorState) hitButton(x, y int32) int {
	e.mu.Lock()
	win := e.win
	e.mu.Unlock()
	if win == nil {
		return -1
	}
	full := winui.ClientRect(win.HWND())
	for i := 0; i < edButtonCount; i++ {
		r := e.buttonRect(i, full)
		if x >= r.Left && x <= r.Right && y >= r.Top && y <= r.Bottom {
			return i
		}
	}
	return -1
}

// buttonRect returns the rectangle of the i-th bottom-bar button.
func (e *editorState) buttonRect(i int, full winui.Rect) winui.Rect {
	top := full.Bottom - edBarH/2 - edBtnH/2
	right := full.Right - edMargin - int32(i)*(edBtnW+edBtnGap)
	return winui.Rect{Left: right - edBtnW, Top: top, Right: right, Bottom: top + edBtnH}
}

// onMove extends the selection while dragging.
func (e *editorState) onMove(x, y int32) {
	e.mu.Lock()
	if !e.dragging {
		e.mu.Unlock()
		return
	}
	e.sel = normalize(winui.Rect{Left: e.anchorX, Top: e.anchorY, Right: x, Bottom: y})
	e.mu.Unlock()
	e.repaint()
}

// onUp finishes the drag, dropping taps too small to be a real selection.
func (e *editorState) onUp(x, y int32) {
	if id := e.hitButton(x, y); id >= 0 {
		e.mu.Lock()
		e.dragging = false
		e.mu.Unlock()
		switch id {
		case edBtnSave:
			e.save()
		case edBtnCopy:
			e.copy()
		case edBtnCancel:
			e.cancel()
		}
		return
	}

	e.mu.Lock()
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

// save crops the selection and hands it to the module.
func (e *editorState) save() {
	r := e.selection()
	if r == nil || e.onSave == nil {
		e.cancel()
		return
	}
	e.onSave(cropImage(e.img, *r))
	e.close()
}

// copy crops the selection and pushes it onto the clipboard.
func (e *editorState) copy() {
	r := e.selection()
	if r == nil || e.onCopy == nil {
		e.cancel()
		return
	}
	e.onCopy(cropImage(e.img, *r))
	e.close()
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

// paint renders the screenshot, the dim mask and the bottom button bar.
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
	e.mu.Unlock()

	// 1. The screenshot itself, stretched over the whole window.
	if img != nil {
		c.Image(full, img)
	}

	// 2. Dim everything outside the selection so it reads as "the crop".
	if sel.Width() > 0 && sel.Height() > 0 {
		mask := winui.Rect{Left: sel.Left, Top: sel.Top, Right: sel.Right, Bottom: sel.Bottom}
		if mask.Left < full.Left {
			mask.Left = full.Left
		}
		if mask.Top < full.Top {
			mask.Top = full.Top
		}
		if mask.Right > full.Right {
			mask.Right = full.Right
		}
		if mask.Bottom > full.Bottom {
			mask.Bottom = full.Bottom
		}
		c.Fill(winui.Rect{Left: full.Left, Top: full.Top, Right: full.Right, Bottom: mask.Top}, edColorDim)
		c.Fill(winui.Rect{Left: full.Left, Top: mask.Bottom, Right: full.Right, Bottom: full.Bottom}, edColorDim)
		c.Fill(winui.Rect{Left: full.Left, Top: mask.Top, Right: mask.Left, Bottom: mask.Bottom}, edColorDim)
		c.Fill(winui.Rect{Left: mask.Right, Top: mask.Top, Right: full.Right, Bottom: mask.Bottom}, edColorDim)
		c.StrokeRect(mask, edColorSel, 1)
	}

	// 3. The button bar along the bottom.
	bar := winui.Rect{Left: full.Left, Top: full.Bottom - edBarH, Right: full.Right, Bottom: full.Bottom}
	c.Fill(bar, edColorSurface)
	c.Line(bar.Left, bar.Top, bar.Right, bar.Top, edColorEdge, 1)

	ready := sel.Width() >= edMinSel && sel.Height() >= edMinSel
	drawButton(c, e.buttonRect(edBtnSave, full), "保存 (Enter)", edColorAccent, ready)
	drawButton(c, e.buttonRect(edBtnCopy, full), "复制 (C)", edColorAccent, ready)
	drawButton(c, e.buttonRect(edBtnCancel, full), "取消 (Esc)", edColorDanger, true)
}

// drawButton paints one bottom-bar button; disabled ones are dimmed.
func drawButton(c *winui.Canvas, r winui.Rect, label string, fill uint32, enabled bool) {
	fg := uint32(edColorText)
	if !enabled {
		fill, fg = edColorEdge, edColorDisabled
	}
	c.Fill(r, fill)
	c.DrawText(label, r, fg, winui.DT_CENTER|winui.DT_VCENTER|winui.DT_SINGLELINE)
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
