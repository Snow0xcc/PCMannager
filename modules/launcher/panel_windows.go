//go:build windows

package launcher

import (
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/sysutil"
	"github.com/snow0xcc/pcmannager/internal/winui"
)

// 面板几何（设备像素）。uTools/dtools 式图标面板：
// 候选磁贴**单行横向排列**，面板宽度固定为屏幕 3/5，超出部分不换行而是
// 裁剪显示，用户向右滚动查看后续候选——不再把整屏占满，也不会因条目多而
// 叠成多行。
//
// panelScale 是全局放大系数：把面板整体（磁贴、间距、内边距、搜索框）
// 同比例放大 4 倍，更醒目易读。唯一不随它缩放的是搜索结果图标的 glyph
// 尺寸（iconGlyphSide）——保持原来的绝对值，避免矢量图标被放大后线条
// 过粗、观感臃肿。
const (
	panelScale int32 = 4 // 整体放大倍数

	gridTileW int32 = 96 * panelScale // 磁贴宽（放大后 384）
	gridTileH int32 = 84 * panelScale // 磁贴高（放大后 336）
	gridGap   int32 = 8 * panelScale  // 磁贴间距
	gridPadX  int32 = 16 * panelScale // 面板左右内边距
	gridPadY  int32 = 10 * panelScale // 面板上下内边距

	panelEditH int32 = 48 * panelScale // 顶部搜索框高度

	// iconGlyphSide 是搜索图标的固定绘制边长，不随 panelScale 放大。
	// 它是原 84px 磁贴下 "84/2-6" 得到的 36px，保持这一绝对值，
	// 让放大后的面板仍用精致的小图标。
	iconGlyphSide int32 = 36

	// 面板字号（点），随 panelScale 放大。
	panelFontPt = 12 * panelScale

	// pinBadgeSide 是右上角图钉热区/绘制边长，不随 panelScale 放大（保持
	// 精致的小角标）。
	pinBadgeSide int32 = 14
)

// 颜色（COLORREF 0x00BBGGRR）。深/浅两套，按系统主题（winui.DarkModeEnabled）
// 在 paint 时选择；不再是写死的深色。
var (
	darkBg     = uint32(0x00221F1B) // 深底（RGB 27,31,34）
	darkEditBg = uint32(0x00302A26)
	darkText   = uint32(0x00F0F0F0)
	darkMuted  = uint32(0x00909090)
	darkAccent = uint32(0x00B85A2A) // 高亮选中（RGB 42,90,184）
	darkRowSel = uint32(0x00402F1F)
	darkPin    = uint32(0x003CA9E5) // 置顶图钉（金色 #E5A93C）
	darkPinOff = uint32(0x00606060) // 未置顶图钉（暗淡，悬停可点）

	lightBg     = uint32(0x00F8F8FA) // 浅底（RGB 250,248,248）
	lightEditBg = uint32(0x00ECEDF0)
	lightText   = uint32(0x00222B3A)
	lightMuted  = uint32(0x00909AA8)
	lightAccent = uint32(0x00B85A2A)
	lightRowSel = uint32(0x00E3EDFC)
	lightPin    = uint32(0x003CA9E5) // 置顶图钉（金色 #E5A93C）
	lightPinOff = uint32(0x00B0B8C0) // 未置顶图钉（暗淡）
)

// panelTheme 是当前生效的一套配色。
type panelTheme struct {
	bg, editBg, text, muted, accent, rowSel, pin, pinOff uint32
}

// currentTheme 按系统明暗选择配色。每次 paint 时读取，面板弹出即跟随
// 当前系统主题。
func currentTheme() panelTheme {
	if winui.DarkModeEnabled() {
		return panelTheme{darkBg, darkEditBg, darkText, darkMuted, darkAccent, darkRowSel, darkPin, darkPinOff}
	}
	return panelTheme{lightBg, lightEditBg, lightText, lightMuted, lightAccent, lightRowSel, lightPin, lightPinOff}
}

// 面板消息。
const (
	lnWmKeyDown     = 0x0100
	lnWmChar        = 0x0102
	lnWmLButtonDown = 0x0201
	lnWmActivate    = 0x0006
	lnWmMouseMove   = 0x0200
	lnWmMouseWheel  = 0x020A
	lnWmRButtonDown = 0x0204

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
	query   string
	sel     int
	rows    []command
	max     int
	ranks   *rankStore
	offsetX int32 // 横向滚动偏移（像素），越界时 clamp
	wheel   int   // 滚轮增量累加器

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
func (p *panelState) present(cmds []command, max int, ranks *rankStore) {
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
			runtime.LockOSThread()
			p.run(cmds, max, ranks, ready)
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
	p.rows = search(cmds, "", max, ranks)
	p.max = max
	p.ranks = ranks
	p.query = ""
	p.sel = 0
	p.offsetX = 0
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
		winui.PostMessage(win.HWND(), winui.WM_CLOSE, 0, 0)
	}
}

// run creates the window and pumps messages for the panel's whole life.
func (p *panelState) run(cmds []command, max int, ranks *rankStore, ready chan<- error) {
	winui.SetDPIAware()

	win, err := winui.NewWindow("GoBoxLauncher", winui.WS_POPUP,
		winui.WS_EX_TOPMOST|winui.WS_EX_TOOLWINDOW, winui.Invalid)
	if err != nil {
		ready <- err
		return
	}
	win.Handle = p.wndProc

	p.mu.Lock()
	p.win = win
	p.rows = search(cmds, "", max, ranks)
	p.max = max
	p.ranks = ranks
	p.font = winui.NewFont("Microsoft YaHei", panelFontPt, winui.FW_NORMAL)
	p.mu.Unlock()

	p.centerAndSize(len(search(cmds, "", max, ranks)))
	ready <- nil

	winui.MessageLoop(nil)

	p.mu.Lock()
	if p.font != 0 {
		winui.DeleteObject(p.font)
		p.font = 0
	}
	p.win = nil
	p.visible = false
	p.mu.Unlock()
}

// panelSize 由候选数与屏幕宽度算出面板的像素宽高。单行布局：高度恒定
// （搜索框 + 一行磁贴），宽度固定为屏幕 3/5。
func panelSize(n int, screenW int32) (w, h int32) {
	if n < 1 {
		n = 1
	}
	w = screenW * 3 / 5
	h = gridPadY*2 + panelEditH + gridTileH
	return
}

// contentWidth 返回全部磁贴铺开后的内容总宽（含两侧内边距）。
func contentWidth(n int) int32 {
	return gridPadX*2 + int32(n)*gridTileW + (int32(n)-1)*gridGap
}

// maxOffsetX 返回横向滚动上限（内容超出面板的部分）。内容不足一屏时为 0。
func (p *panelState) maxOffsetX() int32 {
	p.mu.Lock()
	n := len(p.rows)
	p.mu.Unlock()
	sw, _ := winui.ScreenSize()
	w, _ := panelSize(n, sw)
	extra := contentWidth(n) - w
	if extra < 0 {
		extra = 0
	}
	return extra
}

// clampOffset 把横向偏移限制在 [0, maxOffset] 内。
func (p *panelState) clampOffset() {
	m := p.maxOffsetX()
	if p.offsetX < 0 {
		p.offsetX = 0
	}
	if p.offsetX > m {
		p.offsetX = m
	}
}

// centerAndSize positions the panel centred horizontally and slightly above
// the vertical centre of the primary display.
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
	pw, ph := panelSize(n, sw)

	x := (sw - pw) / 2
	if x < 0 {
		x = 0
	}
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
	case lnWmRButtonDown:
		p.onRowMenu(hwnd, int32(int16(lParam&0xFFFF)), int32(int16(lParam>>16)))
		return 0, true
	case lnWmMouseMove:
		p.onMouseMove(hwnd, int32(int16(lParam&0xFFFF)), int32(int16(lParam>>16)))
		return 0, true
	case lnWmMouseWheel:
		p.onWheel(hwnd, wParam)
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
// 单行布局：左右前后移动，上下无操作。
func (p *panelState) onKey(hwnd winui.HWND, vk int) {
	switch vk {
	case lnVkEscape:
		p.hide()
	case lnVkReturn:
		p.execute(hwnd)
	case lnVkRight:
		p.moveSel(1)
		winui.InvalidateRect(hwnd)
	case lnVkLeft:
		p.moveSel(-1)
		winui.InvalidateRect(hwnd)
	}
}

// onWheel 把鼠标滚轮映射为横向滚动：向上滚（delta>0）看左侧，向下滚看右侧。
// 高分辨率滚轮的细小增量累加后按整格（WHEEL_DELTA）折算成磁贴列宽。
func (p *panelState) onWheel(hwnd winui.HWND, wParam uintptr) {
	delta := int(winui.GET_WHEEL_DELTA_WPARAM(wParam))
	step := int(gridTileW + gridGap)

	p.mu.Lock()
	p.wheel += delta
	// 每累积 120（一物理格）横向移动一个磁贴宽度。
	for p.wheel >= 120 {
		p.offsetX -= int32(step)
		p.wheel -= 120
	}
	for p.wheel <= -120 {
		p.offsetX += int32(step)
		p.wheel += 120
	}
	changed := false
	before := p.offsetX
	p.clampOffset()
	changed = before != p.offsetX
	p.mu.Unlock()

	if changed {
		winui.InvalidateRect(hwnd)
	}
}

// onChar appends/backspaces the query and refreshes candidates.
func (p *panelState) onChar(hwnd winui.HWND, r rune) {
	if r == lnVkBack {
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
	ranks := p.ranks
	p.mu.Unlock()

	rows := search(cmds, q, max, ranks)
	p.mu.Lock()
	p.rows = rows
	p.offsetX = 0
	p.wheel = 0
	if p.sel >= len(rows) {
		p.sel = len(rows) - 1
	}
	if p.sel < 0 {
		p.sel = 0
	}
	p.mu.Unlock()
	p.centerAndSize(len(rows))
}

// tileAt 把客户区坐标映射到磁贴下标（未命中返回 -1）。考虑横向滚动偏移：
// 磁贴 i 的客户区左边缘 = gridPadX - offsetX + i*(gridTileW+gridGap)。
func (p *panelState) tileAt(x, y int32) int {
	p.mu.Lock()
	n := len(p.rows)
	offsetX := p.offsetX
	p.mu.Unlock()
	if n == 0 {
		return -1
	}
	rowTop := gridPadY + panelEditH
	if y < rowTop || y >= rowTop+gridTileH {
		return -1
	}
	if x < gridPadX {
		return -1
	}
	cell := x - gridPadX + offsetX
	col := cell / (gridTileW + gridGap)
	if col < 0 || int(col) >= n {
		return -1
	}
	// 落进磁贴之间的 gap 算未命中。
	cellX := cell - col*(gridTileW+gridGap)
	if cellX >= gridTileW {
		return -1
	}
	return int(col)
}

// onMouseMove 让悬停磁贴跟随高亮。
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

// onRowMenu 弹右键菜单，按目标类型动态呈现菜单项：
//
//	公共项：固定/取消固定、复制路径、打开位置
//	应用/脚本（.exe/.bat/.cmd/.ps1/.lnk）：以管理员身份运行
//	目录（folder）：在终端打开此处
//
// 菜单弹出坐标做边缘防溢出（菜单尺寸为估算值，靠近屏幕右/下边缘时向左/上翻）。
func (p *panelState) onRowMenu(hwnd winui.HWND, x, y int32) {
	idx := p.tileAt(x, y)
	p.mu.Lock()
	var sel command
	have := idx >= 0 && idx < len(p.rows)
	if have {
		sel = p.rows[idx]
	}
	ranks := p.ranks
	p.mu.Unlock()

	// 构造菜单项与动作闭包，二者下标严格对应（分隔符不占动作位）。
	type act struct {
		text string
		fn   func()
	}
	var actions []act
	addSep := false
	add := func(text string, fn func()) {
		actions = append(actions, act{text: text, fn: fn})
	}

	if have {
		pinned := false
		if ranks != nil {
			_, pinned, _ = ranks.get(sel.key())
		}
		pinText := "固定到前方"
		if pinned {
			pinText = "取消固定到前方"
		}
		add(pinText, func() {
			if ranks != nil {
				_, pinnedNow, _ := ranks.get(sel.key())
				ranks.setPin(sel.key(), !pinnedNow)
				p.refreshRows()
				winui.InvalidateRect(hwnd)
			}
		})
		addSep = true

		// 复制路径 / 打开位置 / 终端 / 管理员运行：仅对本地目标（app/file/folder）有意义。
		switch sel.Kind {
		case "app", "file", "folder":
			add("编辑关键字...", func() { p.editAliases(sel) })
			add("复制文件路径", func() {
				if err := winui.ClipboardText(sel.Path); err != nil {
					p.ctx.Bus.Notice(moduleID, "复制路径失败："+err.Error())
				}
			})
			add("打开文件位置", func() {
				if err := sysutil.ShowInFolder(sel.Path); err != nil {
					p.ctx.Bus.Notice(moduleID, "打开位置失败："+err.Error())
				}
			})
			if isExecutablePath(sel.Path) {
				add("以管理员身份运行", func() {
					if err := sysutil.RunElevatedPath(sel.Path, nil); err != nil {
						p.ctx.Bus.Notice(moduleID, "管理员运行失败："+err.Error())
					}
				})
			}
			if sel.Kind == "folder" {
				add("在终端打开此处", func() {
					if err := sysutil.OpenTerminalHere(sel.Path); err != nil {
						p.ctx.Bus.Notice(moduleID, "打开终端失败："+err.Error())
					}
				})
			}
		}
	} else {
		add("清空排行榜", func() {
			if ranks != nil {
				ranks.clear()
				p.refreshRows()
				winui.InvalidateRect(hwnd)
			}
		})
	}

	if len(actions) == 0 {
		return
	}

	// 组装 winui.MenuItem（分隔符插在固定项之后，与 actions 下标错位）。
	items := make([]winui.MenuItem, 0, len(actions)+1)
	for i, a := range actions {
		if addSep && i == 1 {
			items = append(items, winui.MenuItem{Separator: true})
		}
		items = append(items, winui.MenuItem{Text: a.text})
	}

	// 屏幕坐标 + 边缘防溢出：菜单估算尺寸，靠近右/下边缘时向左/上翻。
	sw, sh := winui.ScreenSize()
	mx := x
	my := y
	if x+menuEstW > sw {
		mx = x - menuEstW
	}
	if y+menuEstH > sh {
		my = y - menuEstH
	}
	if mx < 0 {
		mx = 0
	}
	if my < 0 {
		my = 0
	}

	choice := winui.PopupMenu(hwnd, mx, my, items)
	if choice >= 0 && choice < len(actions) {
		p.hide()
		actions[choice].fn()
	}
}

// 菜单尺寸估算（防溢出的折叠阈值）。真实尺寸由系统绘制决定，这里给一个
// 够用的上限：宽度 220px、每项 28px。
const (
	menuEstW = 220
	menuEstH = 400
)

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
	p.feat.markUsed(c)
	go func() {
		if err := p.feat.run(c); err != nil {
			p.ctx.Logger.Warn("快捷面板执行失败", "module", moduleID,
				"cmd", c.Label, "err", err)
			p.ctx.Bus.Notice(moduleID, "执行失败："+c.Label)
		}
	}()
}

// onRowClick selects and runs a clicked tile. 先判右上角图钉热区：点击图钉
// 切换置顶而不执行命令（对标 uTools 的 pin-icon 点击），否则选中并执行。
func (p *panelState) onRowClick(hwnd winui.HWND, x, y int32) {
	idx := p.tileAt(x, y)
	if idx < 0 {
		return
	}
	if p.pinHotAt(idx, x, y) {
		p.togglePinAt(idx)
		return
	}
	p.mu.Lock()
	p.sel = idx
	p.mu.Unlock()
	p.execute(hwnd)
}

// pinHotAt 判断 (x,y) 是否落在磁贴 idx 的右上角图钉热区（一个 pinBadgeSide
// 见方的区域，与 paint 绘制位置一致）。
func (p *panelState) pinHotAt(idx int, x, y int32) bool {
	p.mu.Lock()
	offsetX := p.offsetX
	p.mu.Unlock()
	left := gridPadX - offsetX + int32(idx)*(gridTileW+gridGap)
	rowTop := gridPadY + panelEditH
	// 热区：磁贴右上角 pinBadgeSide×pinBadgeSide。
	hx := left + gridTileW - pinBadgeSide - 2
	hy := rowTop + 2
	return x >= hx && x <= left+gridTileW-2 && y >= hy && y <= hy+pinBadgeSide
}

// togglePinAt 切换磁贴 idx 的置顶状态并刷新。
func (p *panelState) togglePinAt(idx int) {
	p.mu.Lock()
	if idx < 0 || idx >= len(p.rows) {
		p.mu.Unlock()
		return
	}
	c := p.rows[idx]
	ranks := p.ranks
	p.mu.Unlock()
	if ranks == nil {
		return
	}
	_, pinned, _ := ranks.get(c.key())
	ranks.setPin(c.key(), !pinned)
	p.refreshRows()
}

// editAliases 弹出模态输入框让用户编辑候选的别名/拼音首字母缩写。
// 多个别名用逗号或空格分隔；清空并确定即删除全部别名。
// 调用点在窗口线程（右键菜单回调），InputDialog 的嵌套消息循环因此安全。
func (p *panelState) editAliases(c command) {
	ranks := p.ranks
	if ranks == nil || p.win == nil {
		return
	}
	current := strings.Join(ranks.getAliases(c.key()), ", ")
	value, ok := winui.InputDialog(p.win.HWND(), "编辑关键字", c.Label+" 的搜索关键字（逗号分隔，可用拼音缩写）:", current)
	if !ok {
		return
	}
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\t'
	})
	var aliases []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			aliases = append(aliases, f)
		}
	}
	ranks.setAliases(c.key(), aliases)
	p.refreshRows()
}

// paint renders the query box and the single-row tile strip.
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
	offsetX := p.offsetX
	ranks := p.ranks
	p.mu.Unlock()

	c.Fill(rect, theme.bg)

	// 查询框。
	editRect := winui.Rect{Left: gridPadX, Top: 6, Right: rect.Right - gridPadX, Bottom: panelEditH - 4}
	c.Fill(editRect, theme.editBg)
	restore := c.SelectFont(font)
	if query == "" {
		c.DrawText("搜索命令或网址…", editRect.Inset(8), theme.muted,
			winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
	} else {
		c.DrawText(query, editRect.Inset(8), theme.text,
			winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
	}
	restore()

	// 单行磁贴条：横向排列，超出可视区的部分被裁剪（不绘制），滚轮横向滚动。
	rowTop := gridPadY + panelEditH
	// 可视右边界（面板宽）。
	viewRight := rect.Right
	for i, r := range rows {
		left := gridPadX - offsetX + int32(i)*(gridTileW+gridGap)
		if left+gridTileW <= gridPadX || left >= viewRight {
			continue // 完全在可视区外
		}
		tr := winui.Rect{Left: left, Top: rowTop, Right: left + gridTileW, Bottom: rowTop + gridTileH}
		fill := theme.bg
		if i == sel {
			fill = theme.rowSel
		}
		c.Fill(tr, fill)
		if i == sel {
			c.StrokeRect(tr, theme.accent, 1)
		}

		// 图标区：固定 glyph 边长（iconGlyphSide），不随 panelScale 放大。
		iconBox := winui.Rect{
			Left:   tr.Left + (tr.Width()-iconGlyphSide)/2,
			Top:    tr.Top + (tr.Height()*2/5 - iconGlyphSide/2),
			Right:  tr.Left + (tr.Width()+iconGlyphSide)/2,
			Bottom: tr.Top + (tr.Height()*2/5 - iconGlyphSide/2) + iconGlyphSide,
		}
		drawTileIcon(c, iconBox, r.Icon, theme.text)

		// 标签：图标下方单行，超长截断。
		restore2 := c.SelectFont(font)
		c.DrawText(r.Label, winui.Rect{Left: tr.Left + 8, Top: tr.Top + tr.Height()*2/3, Right: tr.Right - 8, Bottom: tr.Bottom - 8},
			theme.text, winui.DT_CENTER|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX|winui.DT_END_ELLIPSIS)
		restore2()

		// 置顶图钉（右上角，常驻）：未置顶暗淡、置顶金色；左键点击热区可切换。
		pinColor := theme.pinOff
		pinned := false
		if ranks != nil {
			_, pinned, _ = ranks.get(r.key())
		}
		if pinned {
			pinColor = theme.pin
		}
		badge := winui.Rect{Left: tr.Right - pinBadgeSide - 2, Top: tr.Top + 2, Right: tr.Right - 2, Bottom: tr.Top + 2 + pinBadgeSide}
		drawPinBadge(c, badge, pinColor)
	}
}

// openURL opens a web shortcut with the platform handler.
func openURL(url string) error { return sysutil.OpenURL(url) }
