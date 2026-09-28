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

// 面板几何（设备像素）。uTools 式图标网格：磁贴横向排列、自动换行，
// 面板宽度只随列数伸缩，不再把整屏占满。
//
// panelScale 是全局放大系数：把面板整体（磁贴、间距、内边距、搜索框）
// 同比例放大 4 倍，更醒目易读。唯一不随它缩放的是搜索结果图标的 glyph
// 尺寸（iconGlyphSide）——保持原来的绝对值，避免矢量图标被放大后线条
// 过粗、观感臃肿。
const (
	// 网格参数（int32，几何计算全程无类型转换）。
	// 列数不再固定，按屏幕宽度动态计算（见 gridColsFor）；
	// 磁贴/间距/内边距/搜索框统一乘 panelScale 放大。
	panelScale int32 = 4 // 整体放大倍数

	gridTileW int32 = 96 * panelScale // 磁贴宽（放大后 384）
	gridTileH int32 = 84 * panelScale // 磁贴高（放大后 336）
	gridGap   int32 = 8 * panelScale  // 磁贴间距
	gridPadX  int32 = 16 * panelScale // 面板左右内边距
	gridPadY  int32 = 10 * panelScale // 面板上下内边距

	panelEditH int32 = 48 * panelScale // 顶部搜索框高度

	// iconGlyphSide 是搜索结果图标的固定绘制边长，不随 panelScale 放大。
	// 它是原 84px 磁贴下 "84/2-6" 得到的 36px，保持这一绝对值，
	// 让放大后的面板仍用精致的小图标。
	iconGlyphSide int32 = 36

	// 面板字号（点），随 panelScale 放大；由 fontSize() 读取。
	panelFontPt = 12 * panelScale
)

// 颜色（COLORREF 0x00BBGGRR）。深/浅两套，按系统主题（winui.DarkModeEnabled）
// 在 paint 时选择；不再是写死的深色。
var (
	// dark 深色主题（默认）。
	darkBg     = uint32(0x00221F1B) // 深底（RGB 27,31,34）
	darkEditBg = uint32(0x00302A26)
	darkText   = uint32(0x00F0F0F0)
	darkMuted  = uint32(0x00909090)
	darkAccent = uint32(0x00B85A2A) // 高亮选中（RGB 42,90,184）
	darkRowSel = uint32(0x00402F1F)

	// light 浅色主题。
	lightBg     = uint32(0x00F8F8FA) // 浅底（RGB 250,248,248）
	lightEditBg = uint32(0x00ECEDF0)
	lightText   = uint32(0x00222B3A)
	lightMuted  = uint32(0x00909AA8)
	lightAccent = uint32(0x00B85A2A) // 选中蓝（RGB 42,90,184）
	lightRowSel = uint32(0x00E3EDFC)
)

// panelTheme 是当前生效的一套配色。
type panelTheme struct {
	bg, editBg, text, muted, accent, rowSel uint32
}

// currentTheme 按系统明暗选择配色。每次 paint 时读取，面板弹出即跟随
// 当前系统主题（无需监听 WM_SETTINGCHANGE，瞬态窗口在弹出时会重新读到）。
func currentTheme() panelTheme {
	if winui.DarkModeEnabled() {
		return panelTheme{darkBg, darkEditBg, darkText, darkMuted, darkAccent, darkRowSel}
	}
	return panelTheme{lightBg, lightEditBg, lightText, lightMuted, lightAccent, lightRowSel}
}

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
	lnVkLeft   = 0x25
	lnVkRight  = 0x27
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
	p.font = winui.NewFont("Microsoft YaHei", panelFontPt, winui.FW_NORMAL)
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

// gridMetrics 由磁贴总数与屏幕宽度算出面板的像素宽高与行列数。
//
// 列数不再是固定的 gridCols，而是「屏幕横向 3/5 能容纳多少个放大后的磁贴」。
// 这样面板总宽恒定占屏宽 60%，磁贴保持放大后的可读尺寸，多余条目自动换行
// 加高——既不小到看不清，也不会把整屏占满。
func gridMetrics(n int, screenW int32) (cols, rows, w, h int32) {
	if n < 1 {
		n = 1
	}
	cols = gridColsFor(n, screenW)
	rows = (int32(n) + cols - 1) / cols
	w = gridPadX*2 + cols*gridTileW + (cols-1)*gridGap
	h = gridPadY*2 + panelEditH + rows*gridTileH + (rows-1)*gridGap
	return
}

// gridColsFor 计算给定条目数下每行的磁贴列数（以屏幕宽度为界）。
func gridColsFor(n int, screenW int32) int32 {
	// 面板最大宽度：屏幕的 3/5。
	maxW := screenW * 3 / 5
	cols := (maxW - gridPadX*2 + gridGap) / (gridTileW + gridGap)
	if cols < 1 {
		cols = 1
	}
	if int32(n) < cols {
		cols = int32(n)
	}
	return cols
}

// centerAndSize positions the panel centred horizontally and slightly above
// the vertical centre of the primary display (the dtools/Spotlight feel:
// the eye's focal point sits in the upper third, not the exact middle).
// 宽高随磁贴行列数伸缩，屏幕不会被一整列占满。
func (p *panelState) centerAndSize(n int) {
	p.mu.Lock()
	max := p.max
	p.mu.Unlock()
	if n > max {
		n = max
	}
	if n < 1 {
		n = 1
	}

	sw, sh := winui.ScreenSize()
	_, _, pw, ph := gridMetrics(n, sw)

	// 水平居中：左右对称。
	x := (sw - pw) / 2
	if x < 0 {
		x = 0
	}
	// 垂直：以屏幕中心为基准，上移 18% 的屏幕高度，落在视觉黄金区。
	y := (sh-ph)/2 - sh*18/100
	if y < 0 {
		y = 0
	}
	p.mu.Lock()
	win := p.win
	p.mu.Unlock()
	if win != nil {
		_ = winui.SetWindowPos(win.HWND(), winui.Invalid,
			int32(x), y, pw, ph,
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
		p.onRowClick(hwnd, int32(int16(lParam&0xFFFF)), int32(int16(lParam>>16)))
		return 0, true
	case lnWmMouseMove:
		p.onMouseMove(hwnd, int32(int16(lParam&0xFFFF)), int32(int16(lParam>>16)))
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
// 二维网格：左右前后移动，上下跨列（uTools 同款键位）。
// 上下移动的步长 = 当前列数（按屏幕宽度动态计算）。
func (p *panelState) onKey(hwnd winui.HWND, vk int) {
	switch vk {
	case lnVkEscape:
		p.hide()
	case lnVkReturn:
		p.execute(hwnd)
	case lnVkDown:
		p.moveSel(int(p.currentCols()))
		winui.InvalidateRect(hwnd)
	case lnVkUp:
		p.moveSel(-int(p.currentCols()))
		winui.InvalidateRect(hwnd)
	case lnVkRight:
		p.moveSel(1)
		winui.InvalidateRect(hwnd)
	case lnVkLeft:
		p.moveSel(-1)
		winui.InvalidateRect(hwnd)
	}
}

// currentCols 返回当前候选列表的列数（与 centerAndSize/paint 一致）。
func (p *panelState) currentCols() int32 {
	p.mu.Lock()
	n := len(p.rows)
	p.mu.Unlock()
	sw, _ := winui.ScreenSize()
	return gridColsFor(n, sw)
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

// tileAt 把客户区坐标映射到磁贴下标（未命中返回 -1）。
func (p *panelState) tileAt(x, y int32) int {
	p.mu.Lock()
	n := len(p.rows)
	p.mu.Unlock()
	if n == 0 {
		return -1
	}
	sw, _ := winui.ScreenSize()
	cols := gridColsFor(n, sw)
	// 先判断行带：磁贴在搜索框下方的网格区。
	rowTop0 := gridPadY + panelEditH
	if y < rowTop0 {
		return -1
	}
	row := (y - rowTop0) / (gridTileH + gridGap)
	col := (x - gridPadX) / (gridTileW + gridGap)
	if col < 0 || col >= cols || x < gridPadX {
		return -1
	}
	// 行内偏移：磁贴之间是 gap，落进 gap 算未命中。
	cellX := x - gridPadX - col*(gridTileW+gridGap)
	cellY := y - rowTop0 - row*(gridTileH+gridGap)
	if cellX >= gridTileW || cellY >= gridTileH {
		return -1
	}
	idx := int(row*cols + col)
	if idx >= n {
		return -1
	}
	return idx
}

// onMouseMove 让悬停磁贴跟随高亮（dtools 式的鼠标/键盘双轨选择）。
func (p *panelState) onMouseMove(hwnd winui.HWND, x, y int32) {
	idx := p.tileAt(x, y)
	if idx < 0 {
		return
	}
	p.mu.Lock()
	changed := idx != p.sel
	p.sel = idx
	p.mu.Unlock()
	if changed {
		winui.InvalidateRect(hwnd)
	}
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

// onRowClick selects and runs a clicked tile.
func (p *panelState) onRowClick(hwnd winui.HWND, x, y int32) {
	idx := p.tileAt(x, y)
	if idx < 0 {
		return
	}
	p.mu.Lock()
	p.sel = idx
	p.mu.Unlock()
	p.execute(hwnd)
}

// tileRect returns the client rectangle of the i-th candidate tile in grid
// coordinates (row-major).
func tileRect(i, cols int32) winui.Rect {
	col := i % cols
	row := i / cols
	left := gridPadX + col*(gridTileW+gridGap)
	top := gridPadY + panelEditH + row*(gridTileH+gridGap)
	return winui.Rect{Left: left, Top: top, Right: left + gridTileW, Bottom: top + gridTileH}
}

// paint renders the query box and the tile grid.
func (p *panelState) paint(hwnd winui.HWND) {
	c, ps := winui.BeginPaint(hwnd)
	if c.DC() == 0 {
		return
	}
	defer winui.EndPaint(hwnd, ps)

	theme := currentTheme()

	rect := winui.ClientRect(hwnd)
	p.mu.Lock()
	query := p.query
	rows := append([]command(nil), p.rows...)
	sel := p.sel
	font := p.font
	p.mu.Unlock()

	c.Fill(rect, theme.bg)

	// 查询框。空查询画灰色占位符；磁贴列表永远要画。
	editRect := winui.Rect{Left: gridPadX, Top: 6, Right: rect.Right - gridPadX, Bottom: panelEditH - 4}
	c.Fill(editRect, theme.editBg)
	restore := c.SelectFont(font)
	if query == "" {
		c.DrawText("搜索命令或网址…", winui.Rect(editRect).Inset(8), theme.muted,
			winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
	} else {
		c.DrawText(query, winui.Rect(editRect).Inset(8), theme.text,
			winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
	}
	restore()

	// 磁贴网格：横向排列、自动换行。每个磁贴 = 图标 + 标签。
	// 列数按屏幕宽度动态计算（与 centerAndSize 一致）。
	sw, _ := winui.ScreenSize()
	cols := gridColsFor(len(rows), sw)
	for i, r := range rows {
		tr := tileRect(int32(i), cols)
		fill := theme.bg
		if i == sel {
			fill = theme.rowSel
		}
		c.Fill(tr, fill)
		if i == sel {
			c.StrokeRect(tr, theme.accent, 1)
		}

		// 图标区：固定 glyph 边长（iconGlyphSide），不随 panelScale 放大。
		// 位置仍随放大后的磁贴居中，保证图标居中观感。
		iconBox := winui.Rect{
			Left:   tr.Left + (tr.Width()-iconGlyphSide)/2,
			Top:    tr.Top + (tr.Height()*2/5 - iconGlyphSide/2),
			Right:  tr.Left + (tr.Width()+iconGlyphSide)/2,
			Bottom: tr.Top + (tr.Height()*2/5 - iconGlyphSide/2) + iconGlyphSide,
		}
		drawTileIcon(c, iconBox, r.Icon, theme.text)

		// 标签：图标下方单行，超长截断。字号随 panelScale 放大。
		restore2 := c.SelectFont(font)
		c.DrawText(r.Label, winui.Rect{Left: tr.Left + 8, Top: tr.Top + tr.Height()*2/3, Right: tr.Right - 8, Bottom: tr.Bottom - 8},
			theme.text, winui.DT_CENTER|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX|winui.DT_END_ELLIPSIS)
		restore2()
	}
}

// openURL opens a web shortcut with the platform handler.
func openURL(url string) error { return sysutil.OpenURL(url) }
