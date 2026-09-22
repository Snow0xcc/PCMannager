//go:build windows

package selfcontext

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// Viewer geometry, in device pixels.
const (
	viewW      = 640
	viewH      = 460
	viewPad    = 12
	listTop    = 34
	listBottom = 46
	rowH       = 20
	btnH       = 24
	btnW       = 88
	btnGap     = 8
)

// Win32 messages the viewer handles that winui does not name.
const (
	wmSetText     = 0x000C
	wmKeyDown     = 0x0100
	wmLButtonDown = 0x0201
	wmMouseWheel  = 0x020A
	vkEscape      = 0x1B
)

// Button ids, laid out right to left along the bottom of the window.
const (
	btnCopy = iota + 1
	btnExport
	btnClear
	btnClose
)

// viewerTitle is the window caption (winui creates the window with none).
const viewerTitle = "PCMannager - 上下文记录"

// buttonOrder is the left-to-right layout of the bottom buttons.
var buttonOrder = []int{btnCopy, btnExport, btnClear, btnClose}

// buttonLabels maps a button id to its caption.
var buttonLabels = map[int]string{
	btnCopy:   "复制摘要",
	btnExport: "导出",
	btnClear:  "清空",
	btnClose:  "关闭",
}

// ctxViewer is the cgo-free context review window.
type ctxViewer struct {
	f *Feature

	mu     sync.Mutex
	win    *winui.Window
	rows   []Entry
	top    int
	closed bool

	hwnd winui.HWND
	font uintptr
}

// viewerMu guards the single active viewer instance.
var (
	viewerMu     sync.Mutex
	activeViewer *ctxViewer
)

// showContext opens the context review window, refreshing the open one.
//
// The window needs an OS thread of its own, so it runs on a dedicated goroutine
// and this call returns as soon as the window is on screen.
func showContext(f *Feature) error {
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

	v := &ctxViewer{f: f}
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
	f.ctx.Logger.Info("已打开上下文记录窗口", "module", moduleID)
	return nil
}

// run creates the window, reports it ready and pumps its messages until the
// window closes. ready receives nil once the window is on screen, or the
// creation error when it could not be created at all.
func (v *ctxViewer) run(ready chan<- error) {
	winui.SetDPIAware()

	w, err := winui.NewWindow("GoBoxContext", winui.WS_POPUP, 0, winui.Invalid)
	if err != nil {
		ready <- errWindowCreate(err)
		return
	}
	v.hwnd = w.HWND()
	if err := winui.MoveWindow(v.hwnd, 0, 0, viewW, viewH, true); err != nil {
		// Non-fatal: the window simply keeps the size it was created with.
		v.f.ctx.Logger.Warn("调整上下文窗口大小失败", "module", moduleID, "err", err)
	}
	v.font = winui.NewFont("Microsoft YaHei", 10, winui.FW_NORMAL)

	w.Handle = v.wndProc
	v.mu.Lock()
	v.win = w
	v.mu.Unlock()
	v.refresh()
	v.setTitle(viewerTitle)
	w.Show()
	ready <- nil

	winui.MessageLoop(nil)
	v.teardown()
}

// errWindowCreate wraps a window creation failure.
func errWindowCreate(err error) error {
	return fmt.Errorf("selfcontext: 创建上下文记录窗口失败: %w", err)
}

// setTitle sets the window caption (winui creates the window with none).
func (v *ctxViewer) setTitle(title string) {
	p, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	winui.SendMessage(v.hwnd, wmSetText, 0, uintptr(unsafe.Pointer(p)))
}

// teardown releases the window's resources exactly once.
func (v *ctxViewer) teardown() {
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
func (v *ctxViewer) isClosed() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.closed
}

// refresh reloads the entries from the module and repaints.
func (v *ctxViewer) refresh() {
	v.mu.Lock()
	v.rows = nil
	if v.f != nil {
		v.rows = v.f.Snapshot()
	}
	v.clampTopLocked()
	v.mu.Unlock()
	v.repaint()
}

// repaint schedules a redraw of the viewer window.
func (v *ctxViewer) repaint() {
	v.mu.Lock()
	hwnd := v.hwnd
	v.mu.Unlock()
	if hwnd.Valid() {
		winui.InvalidateRect(hwnd)
	}
}

// clampTopLocked keeps the scroll offset inside the list. Caller holds mu.
func (v *ctxViewer) clampTopLocked() {
	if max := len(v.rows) - v.visibleRowsLocked(); v.top > max {
		v.top = max
	}
	if v.top < 0 {
		v.top = 0
	}
}

// visibleRowsLocked reports how many rows fit in the list area.
func (v *ctxViewer) visibleRowsLocked() int {
	n := (viewH - listTop - listBottom) / rowH
	if n < 1 {
		return 1
	}
	return n
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
func (v *ctxViewer) activate(id int) {
	switch id {
	case btnCopy:
		v.copySummary()
	case btnExport:
		v.export()
	case btnClear:
		v.f.Clear()
		v.refresh()
	case btnClose:
		v.close()
	}
}

// copySummary writes the summary onto the clipboard and tells the panel.
func (v *ctxViewer) copySummary() {
	if err := copyToClipboard(v.f.Summary()); err != nil {
		v.f.ctx.Logger.Error("复制上下文摘要失败", "module", moduleID, "err", err)
		v.f.ctx.Bus.Notice(moduleID, "复制摘要失败："+err.Error())
		return
	}
	v.f.ctx.Bus.Notice(moduleID, "上下文摘要已复制到剪贴板")
}

// export writes the records to disk and reports the path.
func (v *ctxViewer) export() {
	path, err := v.f.Export()
	if err != nil {
		v.f.ctx.Bus.Notice(moduleID, "导出失败："+err.Error())
		return
	}
	v.f.ctx.Bus.Notice(moduleID, "已导出上下文记录："+path)
}

// close asks the window to shut down.
func (v *ctxViewer) close() {
	v.mu.Lock()
	hwnd := v.hwnd
	v.mu.Unlock()
	if hwnd.Valid() {
		winui.PostMessage(hwnd, winui.WM_CLOSE, 0, 0)
	}
}

// wndProc dispatches the viewer's window messages.
func (v *ctxViewer) wndProc(hwnd winui.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case winui.WM_PAINT:
		winui.DrawWindowText(hwnd, v.paint)
		return 0, true
	case winui.WM_ERASEBKGND:
		return 1, true // the painter fills the background itself
	case wmKeyDown:
		if int(wParam) == vkEscape {
			v.close()
		}
		return 0, true
	case wmLButtonDown:
		if id := buttonAt(int32(int16(lParam&0xFFFF)), int32(int16(lParam>>16))); id != 0 {
			v.activate(id)
		}
		return 0, true
	case wmMouseWheel:
		delta := int32(int16(wParam >> 16))
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
		return 0, true
	case winui.WM_CLOSE, winui.WM_DESTROY:
		v.teardown()
		return 0, true
	}
	return 0, false
}

// paint redraws the whole viewer: a header, the scrollable timeline and the
// bottom action buttons.
func (v *ctxViewer) paint(c *winui.Canvas) {
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
	top := v.top
	v.mu.Unlock()

	flags := uint32(winui.DT_LEFT | winui.DT_SINGLELINE | winui.DT_VCENTER | winui.DT_NOPREFIX)
	c.DrawText(viewHeader(len(rows)), winui.Rect{
		Left: viewPad, Top: viewPad, Right: viewW - viewPad, Bottom: listTop,
	}, winui.ColorPanelFG, flags)

	if len(rows) == 0 {
		c.DrawText(viewEmpty, winui.Rect{
			Left: viewPad, Top: listTop, Right: viewW - viewPad, Bottom: listTop + rowH,
		}, winui.ColorPanelDim, flags|winui.DT_END_ELLIPSIS)
	} else {
		visible := (viewH - listTop - listBottom) / rowH
		for i := 0; i < visible && top+i < len(rows); i++ {
			e := rows[top+i]
			rc := winui.Rect{
				Left:   viewPad,
				Top:    int32(listTop + i*rowH),
				Right:  viewW - viewPad,
				Bottom: int32(listTop + (i+1)*rowH),
			}
			c.DrawText(viewRow(e), rc, winui.ColorPanelFG, flags|winui.DT_END_ELLIPSIS)
		}
	}

	for i, id := range buttonOrder {
		rc := buttonRect(i)
		c.Fill(rc, winui.ColorRowAlt)
		c.DrawText(buttonLabels[id], rc, winui.ColorPanelFG,
			winui.DT_CENTER|winui.DT_SINGLELINE|winui.DT_VCENTER)
	}
}

// viewHeader renders the caption above the timeline.
func viewHeader(count int) string {
	return fmt.Sprintf("上下文记录（%d 条，最近的活动窗口）", count)
}

// viewEmpty is shown when nothing has been recorded yet.
const viewEmpty = "暂无记录：启用模块并工作一会儿后再来看"

// viewRow renders one timeline row: time, title and optional process name.
func viewRow(e Entry) string {
	when := "--:--:--"
	if !e.Timestamp.IsZero() {
		when = e.Timestamp.Format("15:04:05")
	}
	if e.Process != "" {
		return when + "  " + e.Title + "  [" + e.Process + "]"
	}
	return when + "  " + e.Title
}
