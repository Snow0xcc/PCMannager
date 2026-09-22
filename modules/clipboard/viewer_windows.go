//go:build windows

package clipboard

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png" // registers the PNG decoder used for image entry details
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// Viewer geometry, in device pixels.
const (
	viewW      = 660
	viewH      = 440
	viewPad    = 12
	listW      = 330
	listTop    = 34
	listBottom = 46
	rowH       = 22
	btnH       = 24
	btnW       = 96
	btnGap     = 8
)

// Viewer captions.
const (
	viewerTitle = "PCMannager - 剪贴板历史"
	rowEmpty    = "剪贴板历史为空"
	rowHint     = "↑↓ 选择 · Enter 写回 · Del 删除 · P 固定"
)

// dtWordBreak is DT_WORDBREAK, which winui does not name but DrawText needs to
// wrap the preview pane instead of clipping it.
const dtWordBreak = 0x00000010

// Win32 messages the viewer handles that winui does not name.
const (
	wmSetText       = 0x000C
	wmKeyDown       = 0x0100
	wmLButtonDown   = 0x0201
	wmLButtonDblClk = 0x0203
	wmMouseWheel    = 0x020A
	vkUp            = 0x26
	vkDown          = 0x28
	vkReturn        = 0x0D
	vkEscape        = 0x1B
	vkDelete        = 0x2E
	vkP             = 0x50
)

// Button ids, laid out right to left along the bottom of the window.
const (
	btnWrite = iota + 1
	btnPin
	btnDelete
	btnClear
	btnClose
)

// buttonOrder is the left-to-right layout of the bottom buttons.
var buttonOrder = []int{btnWrite, btnPin, btnDelete, btnClear, btnClose}

// buttonLabels maps a button id to its caption.
var buttonLabels = map[int]string{
	btnWrite:  "写回剪贴板",
	btnPin:    "固定/取消",
	btnDelete: "删除",
	btnClear:  "清空",
	btnClose:  "关闭",
}

// clipViewer is the cgo-free clipboard history window.
type clipViewer struct {
	f *Feature

	mu     sync.Mutex
	win    *winui.Window
	rows   []Entry
	sel    int
	top    int
	closed bool

	hwnd winui.HWND
	font uintptr
}

// viewerMu guards the single active viewer instance.
var (
	viewerMu     sync.Mutex
	activeViewer *clipViewer
)

// showViewer opens the clipboard history window, refreshing the open one.
//
// The clipboard watcher pins its own OS thread, so this call must not block on
// a window that needs a thread of its own: the viewer runs on its own
// goroutine and showViewer returns as soon as the window is on screen.
func showViewer(f *Feature) error {
	if f == nil || f.ctx == nil {
		return errNoFeature
	}

	viewerMu.Lock()
	if v := activeViewer; v != nil && !v.isClosed() {
		viewerMu.Unlock()
		v.refresh()
		return nil
	}
	viewerMu.Unlock()

	v := &clipViewer{f: f, sel: -1}
	// ready carries the result of creating and showing the window; the message
	// loop that follows deliberately outlives this call.
	ready := make(chan error, 1)
	go func() {
		// A window belongs to the thread that created it and its messages are
		// only retrievable there, so this goroutine keeps its OS thread for the
		// window's whole lifetime. It never unlocks: letting the goroutine end
		// while still locked would retire the thread and its message queue.
		runtime.LockOSThread()
		v.run(ready)
	}()
	if err := <-ready; err != nil {
		return err
	}
	viewerMu.Lock()
	activeViewer = v
	viewerMu.Unlock()
	f.ctx.Logger.Info("已打开剪贴板历史窗口", "module", moduleID)
	return nil
}

// run creates the window, reports it ready and pumps its messages until the
// window closes. ready receives nil once the window is on screen, or the
// creation error when it could not be created at all.
func (v *clipViewer) run(ready chan<- error) {
	winui.SetDPIAware()

	w, err := winui.NewWindow("GoBoxClipboard", winui.WS_POPUP, 0, winui.Invalid)
	if err != nil {
		ready <- errWindowCreate(err)
		return
	}
	v.hwnd = w.HWND()
	if err := winui.MoveWindow(v.hwnd, 0, 0, viewW, viewH, true); err != nil {
		// Non-fatal: the window simply keeps the size it was created with.
		v.f.ctx.Logger.Warn("调整剪贴板窗口大小失败", "module", moduleID, "err", err)
	}
	v.font = winui.NewFont("Microsoft YaHei", 10, winui.FW_NORMAL)

	w.Handle = v.wndProc
	v.mu.Lock()
	v.win = w
	v.mu.Unlock()
	v.refresh()
	v.setTitle(viewerTitle)
	w.Show() // SW_SHOW activates the window, so it also receives keyboard input
	ready <- nil

	winui.MessageLoop(nil)
	v.teardown()
}

// setTitle sets the window caption (winui creates the window with none).
func (v *clipViewer) setTitle(title string) {
	p, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	winui.SendMessage(v.hwnd, wmSetText, 0, uintptr(unsafe.Pointer(p)))
}

// teardown releases the window's resources exactly once.
func (v *clipViewer) teardown() {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	v.closed = true
	win := v.win
	v.win = nil
	font := v.font
	v.font = 0
	v.mu.Unlock()

	if win != nil {
		win.Destroy()
	}
	if font != 0 {
		winui.DeleteObject(font)
	}
	viewerMu.Lock()
	if activeViewer == v {
		activeViewer = nil
	}
	viewerMu.Unlock()
	winui.PostQuitMessage(0)
}

// isClosed reports whether the viewer has been torn down.
func (v *clipViewer) isClosed() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.closed
}

// refresh reloads the entries from the module and repaints.
func (v *clipViewer) refresh() {
	v.mu.Lock()
	v.rows = nil
	if v.f != nil {
		v.rows = v.f.hist.All()
	}
	switch {
	case len(v.rows) == 0:
		v.sel, v.top = -1, 0
	case v.sel < 0 || v.sel >= len(v.rows):
		v.sel = len(v.rows) - 1 // newest last
	}
	v.clampTopLocked()
	v.mu.Unlock()
	v.repaint()
}

// repaint schedules a redraw of the viewer window.
func (v *clipViewer) repaint() {
	v.mu.Lock()
	hwnd := v.hwnd
	v.mu.Unlock()
	if hwnd.Valid() {
		winui.InvalidateRect(hwnd)
	}
}

// clampTopLocked scrolls the selection into view. Caller holds mu.
func (v *clipViewer) clampTopLocked() {
	visible := v.visibleRowsLocked()
	if v.sel < v.top {
		v.top = v.sel
	}
	if v.sel >= v.top+visible {
		v.top = v.sel - visible + 1
	}
	if max := len(v.rows) - visible; v.top > max {
		v.top = max
	}
	if v.top < 0 {
		v.top = 0
	}
}

// visibleRowsLocked reports how many rows fit in the list area.
func (v *clipViewer) visibleRowsLocked() int {
	n := (viewH - listTop - listBottom) / rowH
	if n < 1 {
		return 1
	}
	return n
}

// selected returns the highlighted entry, or nil when the list is empty.
func (v *clipViewer) selected() *Entry {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.sel < 0 || v.sel >= len(v.rows) {
		return nil
	}
	e := v.rows[v.sel]
	return &e
}

// rowAt maps a client-space y coordinate to a row index (-1 when outside).
func (v *clipViewer) rowAt(y int32) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	if y < listTop || len(v.rows) == 0 {
		return -1
	}
	idx := v.top + int(y-listTop)/rowH
	if idx >= len(v.rows) {
		return -1
	}
	return idx
}

// buttonAt maps a client-space point to a bottom button id (0 when none).
func buttonAt(x, y int32) int {
	top := int32(viewH - viewPad - btnH)
	if y < top || y > top+btnH {
		return 0
	}
	for i, id := range buttonOrder {
		rc := buttonRect(i)
		if x >= rc.Left && x <= rc.Right {
			return id
		}
	}
	return 0
}

// buttonRect returns the rectangle of the i-th bottom button.
func buttonRect(i int) winui.Rect {
	top := int32(viewH - viewPad - btnH)
	right := int32(viewW - viewPad - i*(btnW+btnGap))
	return winui.Rect{Left: right - btnW, Top: top, Right: right, Bottom: top + btnH}
}

// activate runs the action bound to a bottom button.
func (v *clipViewer) activate(id int) {
	switch id {
	case btnWrite:
		v.writeBackSelected()
	case btnPin:
		v.togglePin()
	case btnDelete:
		v.deleteSelected()
	case btnClear:
		v.clearAll()
	case btnClose:
		v.close()
	}
}

// close asks the window to shut down.
func (v *clipViewer) close() {
	v.mu.Lock()
	hwnd := v.hwnd
	v.mu.Unlock()
	if hwnd.Valid() {
		winui.PostMessage(hwnd, winui.WM_CLOSE, 0, 0)
	}
}

// togglePin flips the pinned flag of the selected entry.
func (v *clipViewer) togglePin() {
	e := v.selected()
	if e == nil {
		return
	}
	e.Pinned = !e.Pinned
	v.f.hist.Update(*e)
	v.f.ctx.Bus.State(moduleID, v.f.State())
	v.refresh()
}

// deleteSelected drops the selected entry from the history.
func (v *clipViewer) deleteSelected() {
	e := v.selected()
	if e == nil {
		return
	}
	v.f.hist.Delete(e.ID)
	v.f.ctx.Bus.State(moduleID, v.f.State())
	v.refresh()
}

// clearAll drops every unpinned entry.
func (v *clipViewer) clearAll() {
	v.f.hist.Clear()
	v.f.ctx.Logger.Info("已清空剪贴板历史", "module", moduleID)
	v.f.ctx.Bus.State(moduleID, v.f.State())
	v.refresh()
}

// writeBackSelected pushes the selected entry onto the system clipboard and
// closes the viewer so the user can paste straight into the target window.
//
// The viewer owns the foreground while it is open, so auto-paste is skipped
// here: there would be no reliable target window to receive it.
func (v *clipViewer) writeBackSelected() {
	e := v.selected()
	if e == nil {
		return
	}
	if err := v.f.put(*e, false); err != nil {
		v.f.ctx.Bus.Notice(moduleID, "写回剪贴板失败："+err.Error())
		return
	}
	v.f.ctx.Bus.Progress(moduleID, "write", 100, "已写回剪贴板")
	v.close()
}

// wndProc dispatches the viewer's window messages.
func (v *clipViewer) wndProc(hwnd winui.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case winui.WM_PAINT:
		winui.DrawWindowText(hwnd, v.paint)
		return 0, true
	case winui.WM_ERASEBKGND:
		return 1, true // the painter fills the background itself
	case wmKeyDown:
		v.onKey(int(wParam))
		return 0, true
	case wmLButtonDown:
		v.onClick(int32(int16(lParam&0xFFFF)), int32(int16(lParam>>16)))
		return 0, true
	case wmLButtonDblClk:
		if v.rowAt(int32(int16(lParam>>16))) >= 0 {
			v.writeBackSelected()
		}
		return 0, true
	case wmMouseWheel:
		v.onWheel(int32(int16(wParam >> 16)))
		return 0, true
	case winui.WM_CLOSE, winui.WM_DESTROY:
		v.teardown()
		return 0, true
	}
	return 0, false
}

// onKey handles the viewer's keyboard shortcuts.
func (v *clipViewer) onKey(vk int) {
	switch vk {
	case vkUp:
		v.move(-1)
	case vkDown:
		v.move(1)
	case vkReturn:
		v.writeBackSelected()
	case vkDelete:
		v.deleteSelected()
	case vkP:
		v.togglePin()
	case vkEscape:
		v.close()
	}
}

// move shifts the selection by delta rows, clamped to the list.
func (v *clipViewer) move(delta int) {
	v.mu.Lock()
	if len(v.rows) == 0 {
		v.mu.Unlock()
		return
	}
	if v.sel < 0 {
		v.sel = len(v.rows) - 1
	}
	v.sel += delta
	if v.sel < 0 {
		v.sel = 0
	}
	if v.sel >= len(v.rows) {
		v.sel = len(v.rows) - 1
	}
	v.clampTopLocked()
	v.mu.Unlock()
	v.repaint()
}

// onWheel scrolls the list by the wheel delta (one notch = 120).
func (v *clipViewer) onWheel(delta int32) {
	rows := delta / 120
	if rows == 0 {
		rows = 1
		if delta > 0 {
			rows = -1
		}
	}
	v.mu.Lock()
	v.top += int(rows)
	v.clampTopLocked()
	v.mu.Unlock()
	v.repaint()
}

// onClick handles a left click: either a bottom button or a history row.
func (v *clipViewer) onClick(x, y int32) {
	if id := buttonAt(x, y); id != 0 {
		v.activate(id)
		return
	}
	if idx := v.rowAt(y); idx >= 0 {
		v.mu.Lock()
		v.sel = idx
		v.mu.Unlock()
		v.repaint()
	}
}

// paint redraws the whole viewer.
func (v *clipViewer) paint(c *winui.Canvas) {
	if c.DC() == 0 {
		return
	}
	c.Fill(winui.Rect{Right: viewW, Bottom: viewH}, winui.ColorPanelBG)

	if v.font != 0 {
		restore := c.SelectFont(v.font)
		defer restore()
	}

	v.mu.Lock()
	rows := v.rows
	sel := v.sel
	top := v.top
	v.mu.Unlock()

	c.DrawText(rowHeader(len(rows)), winui.Rect{
		Left: viewPad, Top: viewPad, Right: viewW - viewPad, Bottom: listTop,
	}, winui.ColorPanelFG, winui.DT_LEFT|winui.DT_SINGLELINE|winui.DT_VCENTER|winui.DT_NOPREFIX)

	if len(rows) == 0 {
		c.DrawText(rowEmpty, winui.Rect{
			Left: viewPad, Top: listTop, Right: listW, Bottom: viewH - listBottom,
		}, winui.ColorPanelDim, winui.DT_LEFT|winui.DT_SINGLELINE|winui.DT_VCENTER)
	} else {
		visible := (viewH - listTop - listBottom) / rowH
		for i := 0; i < visible && top+i < len(rows); i++ {
			e := rows[top+i]
			rc := winui.Rect{
				Left:   viewPad,
				Top:    int32(listTop + i*rowH),
				Right:  listW,
				Bottom: int32(listTop + (i+1)*rowH),
			}
			fg := winui.ColorPanelFG
			switch {
			case top+i == sel:
				c.Fill(rc, winui.ColorRowSel)
			case e.Pinned:
				fg = winui.ColorAccent
			}
			c.DrawText(rowLabel(e), rc, fg,
				winui.DT_LEFT|winui.DT_SINGLELINE|winui.DT_VCENTER|winui.DT_END_ELLIPSIS|winui.DT_NOPREFIX)
		}
	}

	// Preview pane on the right.
	preview := winui.Rect{
		Left: listW, Top: listTop, Right: viewW - viewPad, Bottom: viewH - listBottom,
	}
	c.Fill(preview, winui.ColorRowAlt)
	if sel >= 0 && sel < len(rows) {
		inner := preview
		inner.Left += 6
		inner.Top += 6
		inner.Right -= 6
		inner.Bottom -= 6
		c.DrawText(previewText(rows[sel]), inner, winui.ColorPanelFG,
			winui.DT_LEFT|dtWordBreak|winui.DT_NOPREFIX)
	}

	// Bottom hint + buttons.
	hint := winui.Rect{Left: viewPad, Right: listW}
	hint.Top = int32(viewH - viewPad - btnH)
	hint.Bottom = hint.Top + btnH
	c.DrawText(rowHint, hint, winui.ColorPanelDim,
		winui.DT_LEFT|winui.DT_SINGLELINE|winui.DT_VCENTER)

	for i, id := range buttonOrder {
		rc := buttonRect(i)
		c.Fill(rc, winui.ColorRowAlt)
		c.DrawText(buttonLabels[id], rc, winui.ColorPanelFG,
			winui.DT_CENTER|winui.DT_SINGLELINE|winui.DT_VCENTER)
	}
}

// rowHeader renders the caption above the history list.
func rowHeader(count int) string {
	return fmt.Sprintf("剪贴板历史（%d 条）", count)
}

// rowLabel renders the list caption of one entry: time, pin marker and a
// single-line excerpt of its content.
func rowLabel(e Entry) string {
	when := "--:--:--"
	if !e.Timestamp.IsZero() {
		when = e.Timestamp.Format("15:04:05")
	}
	mark := "  "
	if e.Pinned {
		mark = "● "
	}
	return when + " " + mark + rowExcerpt(e)
}

// rowExcerpt returns a short, single-line description of an entry.
func rowExcerpt(e Entry) string {
	if e.Kind == KindImage {
		if w, h, ok := imageSize(e.Data); ok {
			return fmt.Sprintf("[图片 %dx%d]", w, h)
		}
		return fmt.Sprintf("[图片 %d KB]", len(e.Data)/1024)
	}
	return singleLine(e.Text)
}

// previewText renders the right-hand detail pane for the selected entry.
func previewText(e Entry) string {
	if e.Kind == KindImage {
		if w, h, ok := imageSize(e.Data); ok {
			return fmt.Sprintf("[图片 %dx%d]\nPNG，%d 字节\n\n选中“写回剪贴板”即可把图片写回剪贴板。",
				w, h, len(e.Data))
		}
		return fmt.Sprintf("[图片]\nPNG，%d 字节\n\n选中“写回剪贴板”即可把图片写回剪贴板。", len(e.Data))
	}
	if strings.TrimSpace(e.Text) == "" {
		return "(空文本)"
	}
	return e.Text
}

// imageSize decodes an image's dimensions from PNG bytes.
func imageSize(buf []byte) (int, int, bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(buf))
	if err != nil {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// singleLine collapses control characters so an entry fits on one row.
func singleLine(s string) string {
	for _, sep := range []string{"\r\n", "\n", "\r", "\t"} {
		s = strings.ReplaceAll(s, sep, " ")
	}
	return strings.TrimSpace(s)
}
