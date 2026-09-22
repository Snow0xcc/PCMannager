//go:build windows

package taskbar

import (
	"strings"
	"sync"
	"time"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// widget is the TrafficMonitor-style status strip embedded in the taskbar.
//
// It is a small child window parented into Shell_TrayWnd, which is how
// TrafficMonitor keeps its readouts next to the clock: the taskbar owns the
// position, so the widget never competes with real taskbar buttons for space.
type widget struct {
	feat *Feature

	mu      sync.Mutex
	win     *winui.Window
	stats   Stats
	font    uintptr
	fontKey string
	stopped bool
}

// Widget geometry, in device pixels.
const (
	widgetW     = 200
	widgetH     = 22
	widgetPad   = 6
	widgetTimer = 1
)

// newWidget builds the native widget bound to a feature.
func newWidget(f *Feature) *widget {
	return &widget{feat: f}
}

// SetStats updates the numbers to render on the next paint.
func (w *widget) SetStats(s Stats) {
	w.mu.Lock()
	w.stats = s
	win := w.win
	w.mu.Unlock()
	if win != nil {
		winui.InvalidateRect(win.HWND())
	}
}

// Run creates the widget window and pumps its message loop until Stop.
//
// A window belongs to the thread that creates it, so this runs on its own
// locked OS thread for the widget's whole lifetime.
func (w *widget) Run() {
	win, err := winui.NewWindow("GoBoxTaskbar",
		winui.WS_CHILD|winui.WS_CLIPSIBLINGS,
		winui.WS_EX_NOACTIVATE|winui.WS_EX_TOOLWINDOW, 0)
	if err != nil {
		w.feat.reportError("创建任务栏小组件失败", err)
		return
	}
	w.mu.Lock()
	w.win = win
	w.mu.Unlock()

	// Keep the class/callback alive for the loop's lifetime.
	defer win.KeepAlive()
	defer w.releaseFont()

	win.Handle = w.wndProc
	w.layout()
	winui.SetTimer(win.HWND(), widgetTimer, uint32(w.feat.interval().Milliseconds()))
	winui.ShowWindow(win.HWND(), winui.SW_SHOWNA)

	winui.MessageLoop(nil)
}

// wndProc dispatches the widget's window messages.
func (w *widget) wndProc(hwnd winui.HWND, msg uint32, wp, lp uintptr) (uintptr, bool) {
	switch msg {
	case winui.WM_PAINT:
		w.paint(hwnd)
		return 0, true
	case winui.WM_ERASEBKGND:
		return 1, true // the painter fills the background itself
	case winui.WM_TIMER:
		// The collector already pushes new samples; this only guarantees a
		// repaint while the sampler is stalled (e.g. during a suspend).
		winui.InvalidateRect(hwnd)
		return 0, true
	case winui.WM_DESTROY:
		winui.KillTimer(hwnd, widgetTimer)
		winui.PostQuitMessage(0)
		return 0, true
	}
	return 0, false
}

// layout sizes the widget and parents it into the taskbar.
func (w *widget) layout() {
	w.mu.Lock()
	win := w.win
	w.mu.Unlock()
	if win == nil {
		return
	}
	hwnd := win.HWND()

	// Parent first: a child window's coordinates are relative to its parent,
	// so embedding must happen before positioning.
	tb := winui.FindTaskbar()
	if tb.Valid() {
		winui.SetParent(hwnd, tb)
		rect := winui.ClientRect(tb)

		// margin_v shrinks the strip from top and bottom; margin_top then
		// nudges it down, which is how TrafficMonitor lines up with the clock
		// on taskbars that are taller than the default.
		marginV := int32(w.feat.intOpt(optMarginV, defaultMarginV))
		height := int32(widgetH)
		if h := rect.Height() - 2*marginV; h > 0 && h < widgetH {
			height = h
		}
		y := marginV + int32(w.feat.intOpt(optMarginTop, defaultMarginTop))
		if y < 0 {
			y = 0
		}
		if max := rect.Height() - height; max > 0 && y > max {
			y = max
		}
		x := rect.Width() - widgetW - int32(w.feat.offsetX())
		if w.feat.boolOpt(optAvoidWidgets, defaultAvoidWidgets) && x < 0 {
			// Never overlap the notification area: clamp to the left edge.
			x = 0
		}
		_ = winui.MoveWindow(hwnd, x, y, widgetW, height, true)
		return
	}

	// No taskbar (Explorer restarted, or a kiosk shell): park it in the
	// bottom-right corner of the primary display instead.
	sw, sh := winui.ScreenSize()
	_ = winui.SetWindowPos(hwnd, winui.Invalid,
		sw-widgetW-widgetPad, sh-widgetH-widgetPad, widgetW, widgetH,
		winui.SWP_NOZORDER|winui.SWP_SHOWWINDOW|winui.SWP_NOACTIVATE)
}

// paint renders the current stats with GDI.
func (w *widget) paint(hwnd winui.HWND) {
	c, ps := winui.BeginPaint(hwnd)
	if c.DC() == 0 {
		return
	}
	defer winui.EndPaint(hwnd, ps)

	rect := winui.ClientRect(hwnd)
	w.ensureFont()

	w.mu.Lock()
	s := w.stats
	w.mu.Unlock()

	// Background: follow the taskbar accent unless the user pinned a color.
	bg := winui.RGB(32, 33, 36)
	switch w.feat.stringOpt(optBGMode, defaultBGMode) {
	case "solid":
		bg = winui.RGBFromString(w.feat.stringOpt(optBGColor, defaultBGColor), bg)
	case "theme":
		if accent, ok := winui.TaskbarColor(); ok {
			bg = accent
		}
	}
	c.Fill(rect, bg)

	restore := c.SelectFont(w.font)
	defer restore()

	text := w.text(s)
	if text == "" {
		text = "GoBox"
	}
	var flags uint32 = winui.DT_SINGLELINE | winui.DT_VCENTER | winui.DT_NOPREFIX | winui.DT_END_ELLIPSIS
	switch w.feat.stringOpt(optAlign, defaultAlign) {
	case "left":
		flags |= winui.DT_LEFT
	case "center":
		flags |= winui.DT_CENTER
	default:
		flags |= winui.DT_RIGHT
	}
	inner := winui.Rect{
		Left:   rect.Left + widgetPad,
		Top:    rect.Top,
		Right:  rect.Right - widgetPad,
		Bottom: rect.Bottom,
	}
	fg := winui.RGBFromString(w.feat.stringOpt(optFGColor, defaultFGColor), winui.RGB(255, 255, 255))
	c.DrawText(text, inner, fg, flags)
}

// text assembles the single-line readout from the enabled fields.
func (w *widget) text(s Stats) string {
	sep := w.separator()
	parts := make([]string, 0, 6)
	if w.feat.boolOpt(optShowCPU, defaultShowCPU) {
		parts = append(parts, "CPU "+pct(s.CPU))
	}
	if w.feat.boolOpt(optShowMem, defaultShowMem) {
		parts = append(parts, "MEM "+pct(s.RAM))
	}
	if w.feat.boolOpt(optShowDisk, defaultShowDisk) {
		parts = append(parts, "DISK "+pct(s.Disk))
	}
	if w.feat.boolOpt(optShowUptime, defaultShowUptime) {
		parts = append(parts, humanUptime(s.Uptime))
	}
	up := w.feat.boolOpt(optShowUp, defaultShowUp)
	dn := w.feat.boolOpt(optShowDown, defaultShowDown)
	var net strings.Builder
	if up {
		net.WriteString("↑" + w.feat.rate(s.NetUp))
	}
	if dn {
		if net.Len() > 0 {
			net.WriteString(" ")
		}
		net.WriteString("↓" + w.feat.rate(s.NetDn))
	}
	if net.Len() > 0 {
		parts = append(parts, net.String())
	}
	return strings.Join(parts, sep)
}

// separator returns the configured field separator.
func (w *widget) separator() string {
	switch w.feat.stringOpt(optSeparator, defaultSeparator) {
	case "pipe":
		return " | "
	case "dot":
		return " · "
	case "none":
		return ""
	default:
		return "  "
	}
}

// ensureFont (re)creates the GDI font when the configured face/size changes.
func (w *widget) ensureFont() {
	face := w.feat.stringOpt(optFontFamily, defaultFontFamily)
	size := int32(w.feat.intOpt(optFontSize, defaultFontSize))
	key := face + "|" + itoa(int(size))

	w.mu.Lock()
	if w.font != 0 && w.fontKey == key {
		w.mu.Unlock()
		return
	}
	old := w.font
	w.font = winui.NewFont(face, size, winui.FW_NORMAL)
	w.fontKey = key
	w.mu.Unlock()

	if old != 0 {
		winui.DeleteObject(old)
	}
}

// releaseFont frees the font when the message loop exits.
func (w *widget) releaseFont() {
	w.mu.Lock()
	font := w.font
	w.font = 0
	w.fontKey = ""
	w.mu.Unlock()
	if font != 0 {
		winui.DeleteObject(font)
	}
}

// Stop closes the widget window. It is idempotent.
func (w *widget) Stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	win := w.win
	w.mu.Unlock()

	if win != nil {
		win.Destroy()
	}
}

// duration formatting shared by both platform builds.
func humanUptime(d time.Duration) string {
	if d <= 0 {
		return "up --"
	}
	total := int(d.Minutes())
	days, hours, mins := total/1440, (total/60)%24, total%60
	switch {
	case days > 0:
		return "up " + itoa(days) + "d" + itoa(hours) + "h"
	case hours > 0:
		return "up " + itoa(hours) + "h" + itoa(mins) + "m"
	default:
		return "up " + itoa(mins) + "m"
	}
}
