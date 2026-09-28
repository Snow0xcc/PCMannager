//go:build windows

package launcher

import (
	"runtime"
	"sync"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/sysutil"
	"github.com/snow0xcc/pcmannager/internal/winui"
)

// 面板几何（设备像素）。宽而扁、屏幕水平居中略偏上（Spotlight/Alfred 惯例）。
const (
	panelW      = 620
	panelEditH  = 52
	panelRowH   = 40
	panelPadX   = 14
	panelMaxRow = 8

	// 颜色（COLORREF 0x00BBGGRR）。
	panelBg     = 0x00221F1B // 深底（RGB 27,31,34）
	panelEditBg = 0x00302A26
	panelText   = 0x00F0F0F0
	panelMuted  = 0x00909090
	panelAccent = 0x00B85A2A // 高亮选中行（RGB 42,90,184）
	panelRowSel = 0x00402F1F
)

// 面板消息。
const (
	lnWmKeyDown     = 0x0100
	lnWmChar        = 0x0102
	lnWmLButtonDown = 0x0201
	lnWmActivate    = 0x0006
	lnWmMouseMove   = 0x0200

	lnVkReturn = 0x0D
	lnVkEscape = 0x1B
	lnVkUp     = 0x26
	lnVkDown   = 0x28
	lnVkBack   = 0x08
)

// panelState owns the quick panel window. Created lazily on first show; the
// window lives on its own locked thread like every other native window here.
type panelState struct {
	ctx  *core.Context
	feat *Feature

	mu      sync.Mutex
	win     *winui.Window
	visible bool
	closed  bool

	// query/sel/rows 是窗口线程拥有的 UI 状态（消息循环独占访问），
	// 但 present()/hide() 从调用方 goroutine 进来，故仍用 mu 保护。
	query string
	sel   int
	rows  []command
	max   int

	font   uintptr
	fontMx sync.Mutex
}

// newPanel prepares the state; the window itself is created on first present
// (its thread must stay alive afterwards, which the goroutine below guarantees).
func newPanel(ctx *core.Context, f *Feature) *panelState {
	return &panelState{ctx: ctx, feat: f}
}

// visible reports whether the window is on screen.
func (p *panelState) isVisible() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.visible
}

// present pops the panel with the current command table.
//
// The window is created inside the locked goroutine on first call; subsequent
// calls merely re-show it (and refresh the query).
func (p *panelState) present(cmds []command, max int) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return
	}

	p.mu.Lock()
	first := p.win == nil
	p.mu.Unlock()

	if first {
		ready := make(chan error, 1)
		go func() {
			// 窗口属于创建它的线程；为整个生命周期锁住线程（消息只能被
			// 所属线程取到，DestroyWindow 亦然），与项目其它窗口一致。
			runtime.LockOSThread()
			p.run(cmds, max, ready)
		}()
		select {
		case err := <-ready:
			if err != nil {
				p.ctx.Logger.Error("创建快捷面板失败", "module", moduleID, "err", err)
				return
			}
		case <-time.After(3 * time.Second):
			p.ctx.Logger.Error("快捷面板创建超时", "module", moduleID)
			return
		}
	}

	p.mu.Lock()
	// 首次显示：空查询先按默认排序截断出候选，并选中首行——
	// 面板一弹出来就有高亮的第一个可执行项（dtools 式），而不是空列表。
	p.rows = search(cmds, "", max)
	p.max = max
	p.query = ""
	p.sel = 0
	p.visible = true
	win := p.win
	p.mu.Unlock()

	if win != nil {
		p.centerAndSize(len(p.rows))
		win.Show()
		winui.SetForegroundWindow(win.HWND())
		winui.InvalidateRect(win.HWND())
	}
}

// hide dismisses the panel without destroying it (re-show is cheap).
func (p *panelState) hide() {
	p.mu.Lock()
	p.visible = false
	win := p.win
	p.mu.Unlock()
	if win != nil {
		win.Hide()
	}
}

// close destroys the window and ends its thread (module Stop path).
func (p *panelState) close() {
	p.mu.Lock()
	p.closed = true
	p.visible = false
	win := p.win
	p.mu.Unlock()
	if win != nil {
		// PostMessage 跨线程安全；WM_CLOSE 在窗口线程里 Destroy。
		winui.PostMessage(win.HWND(), winui.WM_CLOSE, 0, 0)
	}
}

// run creates the window and pumps messages for the panel's whole life.
func (p *panelState) run(cmds []command, max int, ready chan<- error) {
	winui.SetDPIAware()

	// 无边框 + 置顶 + 工具窗（不进任务栏/Alt+Tab——面板是瞬态的）。
	// WS_EX_NOACTIVATE 绝不能加：面板必须能抢焦点接收键盘输入。
	win, err := winui.NewWindow("GoBoxLauncher", winui.WS_POPUP,
		winui.WS_EX_TOPMOST|winui.WS_EX_TOOLWINDOW, winui.Invalid)
	if err != nil {
		ready <- err
		return
	}
	win.Handle = p.wndProc

	p.mu.Lock()
	p.win = win
	p.rows = search(cmds, "", max)
	p.max = max
	p.font = winui.NewFont("Microsoft YaHei", 12, winui.FW_NORMAL)
	p.mu.Unlock()

	p.centerAndSize(len(search(cmds, "", max)))
	ready <- nil

	winui.MessageLoop(nil)

	// 循环退出（WM_DESTROY）：释放字体并清引用。
	p.mu.Lock()
	if p.font != 0 {
		winui.DeleteObject(p.font)
		p.font = 0
	}
	p.win = nil
	p.visible = false
	p.mu.Unlock()
}

// centerAndSize positions the panel centred horizontally and slightly above
// the vertical centre of the primary display (the dtools/Spotlight feel:
// the eye's focal point sits in the upper third, not the exact middle).
// 高度随候选行数变化；空查询也有默认候选。
func (p *panelState) centerAndSize(rows int) {
	p.mu.Lock()
	max := p.max
	p.mu.Unlock()
	if rows > max {
		rows = max
	}
	if rows < 1 {
		rows = 1
	}
	h := panelEditH + rows*panelRowH + 8

	sw, sh := winui.ScreenSize()
	// 水平居中：左右对称。
	x := (sw - panelW) / 2
	if x < 0 {
		x = 0
	}
	// 垂直：以屏幕中心为基准，上移 18% 的屏幕高度，落在视觉黄金区。
	// 用屏幕中心减去面板高的一半，再整体抬升——既垂直对称又不贴死中央。
	y := (sh-int32(h))/2 - sh*18/100
	if y < 0 {
		y = 0
	}
	p.mu.Lock()
	win := p.win
	p.mu.Unlock()
	if win != nil {
		_ = winui.SetWindowPos(win.HWND(), winui.Invalid,
			int32(x), y, panelW, int32(h),
			winui.SWP_NOZORDER|winui.SWP_NOACTIVATE)
	}
}

// wndProc dispatches panel messages.
func (p *panelState) wndProc(hwnd winui.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case winui.WM_PAINT:
		p.paint(hwnd)
		return 0, true
	case winui.WM_ERASEBKGND:
		return 1, true
	case lnWmActivate:
		// 失焦自动隐藏：wParam 低字为 WA_INACTIVE(0) 时面板失去前台。
		// 这是"弹出即抢焦点、点别处即消失"体验的另一半。
		if int16(wParam&0xFFFF) == 0 {
			p.hide()
			return 0, true
		}
		return 0, false
	case lnWmKeyDown:
		p.onKey(hwnd, int(wParam))
		return 0, true
	case lnWmChar:
		p.onChar(hwnd, rune(wParam))
		return 0, true
	case lnWmLButtonDown:
		p.onRowClick(hwnd, int32(int16(lParam>>16)))
		return 0, true
	case lnWmMouseMove:
		p.onMouseMove(hwnd, int32(int16(lParam>>16)))
		return 0, true
	case winui.WM_CLOSE, winui.WM_DESTROY:
		p.mu.Lock()
		p.visible = false
		p.mu.Unlock()
		if msg == winui.WM_DESTROY {
			winui.PostQuitMessage(0)
		}
		return 0, true
	}
	return 0, false
}

// onKey handles navigation keys; printable input arrives via WM_CHAR.
func (p *panelState) onKey(hwnd winui.HWND, vk int) {
	switch vk {
	case lnVkEscape:
		p.hide()
	case lnVkReturn:
		p.execute(hwnd)
	case lnVkDown:
		p.moveSel(1)
		winui.InvalidateRect(hwnd)
	case lnVkUp:
		p.moveSel(-1)
		winui.InvalidateRect(hwnd)
	}
}

// onChar appends/backspaces the query and refreshes candidates.
func (p *panelState) onChar(hwnd winui.HWND, r rune) {
	if r == lnVkBack { // WM_CHAR 的退格是 0x08
		p.mu.Lock()
		q := []rune(p.query)
		if len(q) > 0 {
			q = q[:len(q)-1]
		}
		p.query = string(q)
		p.sel = 0
		p.mu.Unlock()
	} else if r >= 32 {
		p.mu.Lock()
		p.query += string(r)
		p.sel = 0
		p.mu.Unlock()
	} else {
		return
	}
	p.refreshRows()
	winui.InvalidateRect(hwnd)
}

// moveSel shifts the highlight within bounds.
func (p *panelState) moveSel(d int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.rows)
	if n == 0 {
		return
	}
	p.sel += d
	if p.sel < 0 {
		p.sel = 0
	}
	if p.sel >= n {
		p.sel = n - 1
	}
}

// refreshRows re-runs the search against the live command table.
func (p *panelState) refreshRows() {
	if p.feat == nil {
		return
	}
	p.feat.mu.Lock()
	cmds := p.feat.commands
	p.feat.mu.Unlock()

	p.mu.Lock()
	q := p.query
	max := p.max
	p.mu.Unlock()

	rows := search(cmds, q, max)
	p.mu.Lock()
	p.rows = rows
	if p.sel >= len(rows) {
		p.sel = len(rows) - 1
	}
	if p.sel < 0 {
		p.sel = 0
	}
	p.mu.Unlock()
	p.centerAndSize(len(rows))
}

// onMouseMove 让悬停行跟随高亮（dtools 式的鼠标/键盘双轨选择）。
func (p *panelState) onMouseMove(hwnd winui.HWND, y int32) {
	idx := int((y - panelEditH - 4) / panelRowH)
	p.mu.Lock()
	if idx >= 0 && idx < len(p.rows) && idx != p.sel {
		p.sel = idx
		p.mu.Unlock()
		winui.InvalidateRect(hwnd)
		return
	}
	p.mu.Unlock()
}

// execute runs the highlighted (or clicked) command and dismisses the panel.
func (p *panelState) execute(hwnd winui.HWND) {
	p.mu.Lock()
	sel := p.sel
	rows := append([]command(nil), p.rows...)
	p.mu.Unlock()

	if sel < 0 || sel >= len(rows) {
		return
	}
	c := rows[sel]
	p.hide()
	go func() {
		if err := p.feat.run(c); err != nil {
			p.ctx.Logger.Warn("快捷面板执行失败", "module", moduleID,
				"cmd", c.Label, "err", err)
			p.ctx.Bus.Notice(moduleID, "执行失败："+c.Label)
		}
	}()
}

// onRowClick selects and runs a clicked row.
func (p *panelState) onRowClick(hwnd winui.HWND, y int32) {
	idx := int((y - panelEditH - 4) / panelRowH)
	p.mu.Lock()
	if idx < 0 || idx >= len(p.rows) {
		p.mu.Unlock()
		return
	}
	p.sel = idx
	p.mu.Unlock()
	p.execute(hwnd)
}

// rowRect returns the client rectangle of the i-th candidate row.
func rowRect(i int, left, right int32) winui.Rect {
	top := int32(panelEditH + 4 + i*panelRowH)
	return winui.Rect{Left: left, Top: top, Right: right, Bottom: top + panelRowH}
}

// paint renders the query box and the candidate list.
func (p *panelState) paint(hwnd winui.HWND) {
	c, ps := winui.BeginPaint(hwnd)
	if c.DC() == 0 {
		return
	}
	defer winui.EndPaint(hwnd, ps)

	rect := winui.ClientRect(hwnd)
	p.mu.Lock()
	query := p.query
	rows := append([]command(nil), p.rows...)
	sel := p.sel
	font := p.font
	p.mu.Unlock()

	c.Fill(rect, panelBg)

	// 查询框。空查询时画灰色占位符；候选列表永远要画（此前空查询时提前
	// return 导致列表不渲染，是面板“弹出来却是空白”的根因）。
	editRect := winui.Rect{Left: panelPadX, Top: 6, Right: rect.Right - panelPadX, Bottom: panelEditH - 4}
	c.Fill(editRect, panelEditBg)
	restore := c.SelectFont(font)
	if query == "" {
		c.DrawText("搜索命令或网址…", winui.Rect(editRect).Inset(8), panelMuted,
			winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
	} else {
		c.DrawText(query, winui.Rect(editRect).Inset(8), panelText,
			winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
	}
	restore()

	// 候选行。
	for i, r := range rows {
		rr := rowRect(i, panelPadX, rect.Right-panelPadX)
		fill := uint32(panelBg)
		if i == sel {
			fill = panelRowSel
		}
		c.Fill(rr, fill)
		restore2 := c.SelectFont(font)
		c.DrawText(r.Label, winui.Rect{Left: rr.Left + 8, Top: rr.Top, Right: rr.Left + panelW/2, Bottom: rr.Bottom},
			panelText, winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX|winui.DT_END_ELLIPSIS)
		c.DrawText(r.Hint, winui.Rect{Left: rr.Left + panelW/2 + 8, Top: rr.Top, Right: rr.Right - 8, Bottom: rr.Bottom},
			panelMuted, winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX|winui.DT_END_ELLIPSIS)
		restore2()
	}
}

// openURL opens a web shortcut with the platform handler.
func openURL(url string) error { return sysutil.OpenURL(url) }
