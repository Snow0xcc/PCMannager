//go:build windows

package launcher

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/winui"
)

// 超级面板：全局悬浮磁贴池。显示被「固定到超级面板」的命令，单击执行、
// 右键移出。与搜索面板（panelState）并存但独立窗口/线程。
//
// 布局：单行磁贴条（复用搜索面板的磁贴尺寸与图标绘制），无搜索框，底部
// 一条提示文字。失焦自动隐藏（与搜索面板一致）。
const (
	superPadX   int32 = 12
	superPadY   int32 = 10
	superHintH  int32 = 22
	superFontPt       = 11
)

// superPanelState 拥有超级面板窗口。惰性创建；窗口独占一个锁线程。
type superPanelState struct {
	ctx  *core.Context
	feat *Feature

	mu      sync.Mutex
	win     *winui.Window
	visible bool
	closed  bool
	rows    []command

	font   uintptr
	offset int32 // 横向滚动偏移
	wheel  int
}

func newSuperPanel(ctx *core.Context, f *Feature) *superPanelState {
	return &superPanelState{ctx: ctx, feat: f}
}

func (p *superPanelState) isVisible() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.visible
}

// present 弹出超级面板：用当前活命令表反查固定 key，展示仍然有效的条目。
func (p *superPanelState) present(keys []string) {
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
			p.run(ready)
		}()
		select {
		case err := <-ready:
			if err != nil {
				p.ctx.Logger.Error("创建超级面板失败", "module", moduleID, "err", err)
				return
			}
		case <-time.After(3 * time.Second):
			p.ctx.Logger.Error("超级面板创建超时", "module", moduleID)
			return
		}
	}

	p.mu.Lock()
	p.rows = p.feat.resolveKeys(keys)
	p.offset = 0
	p.visible = true
	win := p.win
	p.mu.Unlock()

	if win != nil {
		p.sizeAndCenter(len(p.rows))
		win.Show()
		winui.SetForegroundWindow(win.HWND())
		winui.InvalidateRect(win.HWND())
	}
}

func (p *superPanelState) hide() {
	p.mu.Lock()
	p.visible = false
	win := p.win
	p.mu.Unlock()
	if win != nil {
		win.Hide()
	}
}

// ToggleSuper 切换超级面板显示（热键与动作入口）：可见则隐藏，否则弹出。
// 非 running 状态报错（与搜索面板 OpenUI 同一守卫语义）。
func (f *Feature) ToggleSuper() error {
	if f.ctx == nil {
		return errNoFeature
	}
	f.mu.Lock()
	s := f.super
	running := f.running
	f.mu.Unlock()

	if !running {
		return fmt.Errorf("launcher: 模块未运行")
	}
	if s != nil && s.isVisible() {
		s.hide()
		return nil
	}
	return f.showSuper()
}

// showSuper 创建（一次）并显示超级面板。
func (f *Feature) showSuper() error {
	f.mu.Lock()
	s := f.super
	f.mu.Unlock()

	if s == nil {
		s = newSuperPanel(f.ctx, f)
		f.mu.Lock()
		f.super = s
		f.mu.Unlock()
	}
	s.present(f.superKeys())
	return nil
}

func (p *superPanelState) close() {
	p.mu.Lock()
	p.closed = true
	p.visible = false
	win := p.win
	p.mu.Unlock()
	if win != nil {
		winui.PostMessage(win.HWND(), winui.WM_CLOSE, 0, 0)
	}
}

// run 创建窗口并泵消息（窗口线程生命周期）。
func (p *superPanelState) run(ready chan<- error) {
	winui.SetDPIAware()
	win, err := winui.NewWindow("GoBoxSuperPanel", winui.WS_POPUP,
		winui.WS_EX_TOPMOST|winui.WS_EX_TOOLWINDOW, winui.Invalid)
	if err != nil {
		ready <- err
		return
	}
	win.Handle = p.wndProc

	p.mu.Lock()
	p.win = win
	p.font = winui.NewFont("Microsoft YaHei", superFontPt, winui.FW_NORMAL)
	p.mu.Unlock()

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

// superSize 由条目数算出面板像素宽高：单行磁贴 + 底部提示条。
// 条目少时面板收缩（不铺满 3/5 屏），条目多时上限 3/5 屏宽可滚动。
func superSize(n int, screenW int32) (w, h int32) {
	if n < 1 {
		n = 1
	}
	content := superPadX*2 + int32(n)*gridTileW + (int32(n)-1)*gridGap
	w = content
	if max := screenW * 3 / 5; w > max {
		w = max
	}
	h = superPadY*2 + gridTileH + superHintH
	return
}

// superContentWidth 返回磁贴内容总宽。
func superContentWidth(n int) int32 {
	if n < 1 {
		n = 1
	}
	return superPadX*2 + int32(n)*gridTileW + (int32(n)-1)*gridGap
}

func (p *superPanelState) maxOffset() int32 {
	p.mu.Lock()
	n := len(p.rows)
	p.mu.Unlock()
	sw, _ := winui.ScreenSize()
	w, _ := superSize(n, sw)
	extra := superContentWidth(n) - w
	if extra < 0 {
		extra = 0
	}
	return extra
}

func (p *superPanelState) clampOffset() {
	if p.offset < 0 {
		p.offset = 0
	}
	if m := p.maxOffset(); p.offset > m {
		p.offset = m
	}
}

// sizeAndCenter 面板居中（水平对称，垂直略偏上，与搜索面板一致的视觉锚点）。
func (p *superPanelState) sizeAndCenter(n int) {
	sw, sh := winui.ScreenSize()
	pw, ph := superSize(n, sw)
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
		_ = winui.SetWindowPos(win.HWND(), winui.Invalid, x, y, pw, ph,
			winui.SWP_NOZORDER|winui.SWP_NOACTIVATE)
	}
}

func (p *superPanelState) wndProc(hwnd winui.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	if res, handled := winui.DispatchOwnerDraw(msg, wParam, lParam); handled {
		return res, true
	}
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
		switch int(wParam) {
		case lnVkEscape:
			p.hide()
		}
		return 0, true
	case lnWmLButtonDown:
		p.onClick(hwnd, int32(int16(lParam&0xFFFF)), int32(int16(lParam>>16)))
		return 0, true
	case lnWmRButtonDown:
		p.onMenu(hwnd, int32(int16(lParam&0xFFFF)), int32(int16(lParam>>16)))
		return 0, true
	case lnWmMouseWheel:
		delta := int(winui.GET_WHEEL_DELTA_WPARAM(wParam))
		step := int(gridTileW + gridGap)
		p.mu.Lock()
		p.wheel += delta
		for p.wheel >= 120 {
			p.offset -= int32(step)
			p.wheel -= 120
		}
		for p.wheel <= -120 {
			p.offset += int32(step)
			p.wheel += 120
		}
		before := p.offset
		p.clampOffset()
		changed := before != p.offset
		p.mu.Unlock()
		if changed {
			winui.InvalidateRect(hwnd)
		}
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

// tileAt 把客户区坐标映射到磁贴下标（未命中 -1）。
func (p *superPanelState) tileAt(x, y int32) int {
	p.mu.Lock()
	n := len(p.rows)
	offset := p.offset
	p.mu.Unlock()
	if n == 0 {
		return -1
	}
	if y < superPadY || y >= superPadY+gridTileH {
		return -1
	}
	if x < superPadX {
		return -1
	}
	cell := x - superPadX + offset
	col := cell / (gridTileW + gridGap)
	if col < 0 || int(col) >= n {
		return -1
	}
	if cellX := cell - col*(gridTileW+gridGap); cellX >= gridTileW {
		return -1
	}
	return int(col)
}

func (p *superPanelState) onClick(hwnd winui.HWND, x, y int32) {
	idx := p.tileAt(x, y)
	if idx < 0 {
		return
	}
	p.mu.Lock()
	if idx >= len(p.rows) {
		p.mu.Unlock()
		return
	}
	c := p.rows[idx]
	p.mu.Unlock()

	p.hide()
	p.feat.markUsed(c)
	go func() {
		if err := p.feat.run(c); err != nil {
			p.ctx.Logger.Warn("超级面板执行失败", "module", moduleID,
				"cmd", c.Label, "err", err)
			p.ctx.Bus.Notice(moduleID, "执行失败："+c.Label)
		}
	}()
}

// onMenu 右键菜单：移出超级面板 / 清空超级面板。
func (p *superPanelState) onMenu(hwnd winui.HWND, x, y int32) {
	idx := p.tileAt(x, y)
	p.mu.Lock()
	var sel command
	have := idx >= 0 && idx < len(p.rows)
	if have {
		sel = p.rows[idx]
	}
	p.mu.Unlock()

	var items []winui.MenuItem
	if have {
		items = append(items, winui.MenuItem{Text: "移出超级面板", Icon: menuIconTrash})
	}
	items = append(items, winui.MenuItem{Text: "清空超级面板", Icon: menuIconTrash})

	sw, sh := winui.ScreenSize()
	mx, my := x, y
	if x+menuEstW > sw {
		mx = x - menuEstW
	}
	if y+200 > sh {
		my = y - 200
	}
	if mx < 0 {
		mx = 0
	}
	if my < 0 {
		my = 0
	}

	choice := winui.PopupMenu(hwnd, mx, my, items)
	switch {
	case have && choice == 0:
		p.feat.removeSuper(sel)
		p.refresh()
	case choice == 1 || (!have && choice == 0):
		p.feat.clearSuper()
		p.refresh()
	}
}

// refresh 重新反查固定项并刷新显示。
func (p *superPanelState) refresh() {
	p.mu.Lock()
	win := p.win
	p.mu.Unlock()
	if win == nil {
		return
	}
	keys := p.feat.superKeys()
	p.mu.Lock()
	p.rows = p.feat.resolveKeys(keys)
	p.offset = 0
	n := len(p.rows)
	p.mu.Unlock()
	p.sizeAndCenter(n)
	winui.InvalidateRect(win.HWND())
}

// paint 渲染磁贴条与底部提示。
func (p *superPanelState) paint(hwnd winui.HWND) {
	c, ps := winui.BeginPaint(hwnd)
	if c.DC() == 0 {
		return
	}
	defer winui.EndPaint(hwnd, ps)

	theme := currentTheme()
	rect := winui.ClientRect(hwnd)
	p.mu.Lock()
	rows := append([]command(nil), p.rows...)
	font := p.font
	offset := p.offset
	p.mu.Unlock()

	c.Fill(rect, theme.bg)

	viewRight := rect.Right
	for i, r := range rows {
		left := superPadX - offset + int32(i)*(gridTileW+gridGap)
		if left+gridTileW <= superPadX || left >= viewRight {
			continue
		}
		tr := winui.Rect{Left: left, Top: superPadY, Right: left + gridTileW, Bottom: superPadY + gridTileH}
		c.Fill(tr, theme.editBg)
		c.StrokeRect(tr, theme.muted, 1)

		iconBox := winui.Rect{
			Left:   tr.Left + (tr.Width()-iconGlyphSide)/2,
			Top:    tr.Top + (tr.Height()*2/5 - iconGlyphSide/2),
			Right:  tr.Left + (tr.Width()+iconGlyphSide)/2,
			Bottom: tr.Top + (tr.Height()*2/5 - iconGlyphSide/2) + iconGlyphSide,
		}
		drawTileIcon(c, iconBox, r.Icon, theme.text)

		restore := c.SelectFont(font)
		c.DrawText(r.Label, winui.Rect{Left: tr.Left + 8, Top: tr.Top + tr.Height()*2/3, Right: tr.Right - 8, Bottom: tr.Bottom - 8},
			theme.text, winui.DT_CENTER|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX|winui.DT_END_ELLIPSIS)
		restore()
	}

	// 底部提示条。
	hintRect := winui.Rect{Left: superPadX, Top: rect.Bottom - superHintH, Right: rect.Right - superPadX, Bottom: rect.Bottom - 4}
	restore := c.SelectFont(font)
	c.DrawText("单击启动 · 右键管理 · 失焦隐藏", hintRect, theme.muted,
		winui.DT_CENTER|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
	restore()
}
