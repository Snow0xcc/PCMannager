//go:build windows

package taskbar

import (
	"runtime"
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

	mu         sync.Mutex
	win        *winui.Window
	stats      Stats
	font       uintptr
	fontKey    string
	auxFont    uintptr
	auxFontKey string
	stopped    bool

	// bgColor is the taskbar pixel colour sampled during layout. Caching it
	// here keeps paint free of screen reads (which would otherwise read back the
	// widget's own pixels and feed back on itself).
	bgColor uint32
	hasBG   bool

	// lastX/lastY/lastW/lastH remember the last applied placement so relayout
	// only moves the window when the target rectangle actually changed.
	lastX, lastY, lastW, lastH int32
	hasLayout                  bool

	// parented records that SetParent has already run. Reparenting is a
	// cross-process operation into Explorer, so it must happen once per window
	// rather than on every reposition.
	parented bool

	// transparent records whether the colour key was accepted, so paint knows
	// to fill with widgetKeyColor (invisible) instead of an opaque background.
	transparent bool
}

// Widget geometry, in device pixels.
const (
	widgetW    = 220
	widgetMinH = 16
	// widgetPad keeps the text off the widget edges; the left side gets extra
	// room because the strip is usually right-aligned, and the readout should not
	// visually collide with the neighbouring taskbar buttons.
	widgetPad   = 8
	widgetTimer = 1

	// widgetKeyColor is the transparent colour key. Magenta is chosen because a
	// taskbar is never painted in it, and the text this widget draws is black or
	// white, so nothing visible is ever keyed out by accident.
	widgetKeyColor = 0x00FF00FF

	// widgetProbeGap keeps the background sample outside the widget's own
	// rectangle, so it reads taskbar pixels rather than the key colour.
	widgetProbeGap = 8

	// widgetLayoutTimer repositions the strip; widgetLayoutMS is deliberately
	// slower than the sampling interval because each check walks Explorer's
	// taskbar children cross-process.
	widgetLayoutTimer = 2
	widgetLayoutMS    = 2000

	// widgetDestroyTimeout bounds how long Stop waits for the window to go away.
	widgetDestroyTimeout = time.Second
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
// A window belongs to the thread that creates it, so this goroutine pins its OS
// thread for the widget's whole lifetime and never unlocks.
func (w *widget) Run() {
	// This is load-bearing, not stylistic. The widget is a cross-process child
	// of Explorer's Shell_TrayWnd, and Windows delivers messages to a window on
	// its OWNING thread only. Without the pin the Go scheduler can migrate this
	// goroutine to another thread, so the pump no longer runs on the window's
	// thread. Explorer then blocks forever inside the synchronous SendMessage it
	// issues to this child (WM_PAINT / WM_ERASEBKGND / WM_WINDOWPOSCHANGED…),
	// which freezes the entire taskbar — the reported symptom.
	//
	// Ending the goroutine while still locked would retire the thread and its
	// message queue, so there is deliberately no UnlockOSThread.
	runtime.LockOSThread()

	// WS_CHILD requires a real parent: CreateWindowEx rejects a child window
	// with a NULL parent ("Cannot create a top-level child window"). Embedding
	// into the taskbar therefore means parenting to Shell_TrayWnd itself.
	//
	// If the taskbar cannot be found (Explorer restarting, or a non-standard
	// shell), fall back to an owned top-level tool window instead of failing:
	// WS_CHILD with a NULL parent can never succeed, so the style must change
	// too, not just the parent.
	parent := winui.FindTaskbar()
	style := uint32(winui.WS_CHILD | winui.WS_CLIPSIBLINGS)
	// WS_EX_LAYERED + a colour key is what makes the background genuinely
	// transparent: a plain GDI child window does not composite, so simply not
	// painting would leave stale pixels instead of showing the taskbar.
	exStyle := uint32(winui.WS_EX_NOACTIVATE | winui.WS_EX_TOOLWINDOW | winui.WS_EX_LAYERED)

	if !parent.Valid() {
		w.feat.reportError("未找到任务栏窗口，改用悬浮小组件", errNoTaskbar)
		parent = 0
		style = winui.WS_POPUP | winui.WS_CLIPSIBLINGS
		exStyle = winui.WS_EX_NOACTIVATE | winui.WS_EX_TOOLWINDOW | winui.WS_EX_TOPMOST
	}

	win, err := winui.NewWindow("GoBoxTaskbar", style, exStyle, parent)
	if err != nil {
		w.feat.reportError("创建任务栏小组件失败", err)
		return
	}

	// The colour key is applied unconditionally: transparent is the default and
	// the mode paint() honours, and a failed call simply degrades to the opaque
	// sampled background.
	transparent := winui.SetColorKey(win.HWND(), widgetKeyColor)

	w.mu.Lock()
	w.win = win
	w.parented = parent.Valid()
	w.transparent = transparent
	w.mu.Unlock()

	// Keep the class/callback alive for the loop's lifetime.
	defer win.KeepAlive()
	defer w.releaseFont()

	win.Handle = w.wndProc
	w.layout()
	winui.SetTimer(win.HWND(), widgetTimer, uint32(w.feat.interval().Milliseconds()))
	// A slower timer drives repositioning: it queries the taskbar's children
	// cross-process, which should not run at the sampling rate.
	winui.SetTimer(win.HWND(), widgetLayoutTimer, uint32(widgetLayoutMS))
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
		switch wp {
		case widgetLayoutTimer:
			// The tray (TrayNotifyWnd) grows and shrinks as icons appear and
			// disappear, so a position computed once at startup drifts under the
			// clock. Re-checking keeps the strip pinned beside it, which is how
			// TrafficMonitor stays aligned.
			w.relayout()
		default:
			// Fresh samples arrived; just repaint.
			winui.InvalidateRect(hwnd)
		}
		// Re-raise every tick: the taskbar's XAML composition layer is managed by
		// Explorer and can end up above the widget again (theme change, DPI
		// change, Explorer re-compositing). SetWindowPos is cheap, and staying
		// hidden is the failure mode we cannot accept.
		winui.BringToTop(hwnd)
		return 0, true
	case winui.WM_CLOSE:
		// Destroy from the window's OWN thread: DestroyWindow is not safe to call
		// cross-thread, and the previous code did exactly that from Stop, which
		// silently left windows behind (three stacked copies were observed).
		winui.DestroyWindow(hwnd)
		return 0, true
	case winui.WM_DESTROY:
		winui.KillTimer(hwnd, widgetTimer)
		winui.KillTimer(hwnd, widgetLayoutTimer)
		winui.PostQuitMessage(0)
		return 0, true
	}
	return 0, false
}

// layout sizes the widget and parents it into the taskbar.
//
// Positioning follows TrafficMonitor: the strip sits immediately to the LEFT of
// the notification area (the clock), not at the taskbar's right edge. Anchoring
// to the right edge (width - widgetW - offset) buried the widget under the clock
// whenever the tray was wide, which is why it looked wrong.
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
	m := winui.TaskbarMetricsNow()
	if m.HWND.Valid() {
		// Reparenting is a cross-process call into Explorer and is only needed
		// when the taskbar window actually changed (first run, or an Explorer
		// restart). Doing it on every reposition made the taskbar churn.
		w.mu.Lock()
		needsParent := !w.parented
		w.mu.Unlock()
		if needsParent {
			winui.SetParent(hwnd, m.HWND)
			w.mu.Lock()
			w.parented = true
			w.mu.Unlock()
		}

		// Full-height strip, matching how TrafficMonitor fills the taskbar
		// vertically; margin_v then shrinks it symmetrically if the user asks.
		marginV := int32(w.feat.intOpt(optMarginV, defaultMarginV))
		height := m.Height - 2*marginV
		if height < widgetMinH {
			height = widgetMinH
		}
		if height > m.Height {
			height = m.Height
		}
		y := marginV + int32(w.feat.intOpt(optMarginTop, defaultMarginTop))
		if y < 0 {
			y = 0
		}
		if max := m.Height - height; max > 0 && y > max {
			y = max
		}

		width := int32(widgetW)
		if f := int32(w.feat.intOpt(optWidth, defaultWidth)); f > 0 {
			width = f
		}

		x := w.horizontalPos(m, width)

		// Move WITHOUT the repaint flag: repaint=true asks the taskbar (a
		// different process) to redraw, and doing that from our timer was a
		// source of taskbar churn. Our own window is refreshed explicitly.
		_ = winui.MoveWindow(hwnd, x, y, width, height, false)

		// Raise above the taskbar's XAML composition layer. On Windows 11 the
		// taskbar hosts a Windows.UI.Composition.DesktopWindowContentBridge child
		// that spans the WHOLE bar and sits above sibling child windows, so an
		// embedded widget stays invisible without this (measured: the window was
		// IsWindowVisible=true yet WindowFromPoint missed it and captures showed
		// bare taskbar).
		winui.BringToTop(hwnd)
		winui.InvalidateRect(hwnd)

		w.mu.Lock()
		w.lastX, w.lastY, w.lastW, w.lastH = x, y, width, height
		w.hasLayout = true
		w.mu.Unlock()

		// Sample what the taskbar is actually painted with at this spot, so
		// the readout can contrast with the real background. Sampled after
		// MoveWindow so the coordinates are the final ones; the sample is taken
		// to the left of the widget so it reads taskbar pixels, not our own.
		w.sampleBackground(m, x, y, height)
		return
	}

	// No taskbar (Explorer restarted, or a kiosk shell): park it in the
	// bottom-right corner of the primary display instead.
	sw, sh := winui.ScreenSize()
	_ = winui.SetWindowPos(hwnd, winui.Invalid,
		sw-widgetW-widgetPad, sh-widgetMinH-widgetPad, widgetW, widgetMinH,
		winui.SWP_NOZORDER|winui.SWP_SHOWWINDOW|winui.SWP_NOACTIVATE)
}

// relayout repositions the widget only when the target rectangle changed.
//
// It runs from the timer so the strip follows the tray as it grows and shrinks
// with the notification icons; the change check keeps a steady taskbar from
// being MoveWindow'd every tick (which would flicker).
func (w *widget) relayout() {
	m := winui.TaskbarMetricsNow()
	if !m.HWND.Valid() {
		return
	}

	width := int32(widgetW)
	if f := int32(w.feat.intOpt(optWidth, defaultWidth)); f > 0 {
		width = f
	}
	marginV := int32(w.feat.intOpt(optMarginV, defaultMarginV))
	height := m.Height - 2*marginV
	if height < widgetMinH {
		height = widgetMinH
	}
	if height > m.Height {
		height = m.Height
	}
	y := marginV + int32(w.feat.intOpt(optMarginTop, defaultMarginTop))
	if y < 0 {
		y = 0
	}
	if max := m.Height - height; max > 0 && y > max {
		y = max
	}
	x := w.horizontalPos(m, width)

	w.mu.Lock()
	win := w.win
	same := w.hasLayout && w.lastX == x && w.lastY == y && w.lastW == width && w.lastH == height
	w.mu.Unlock()
	if win == nil || same {
		return
	}
	w.layout()
}

// horizontalPos computes the widget's x offset inside the taskbar.
//
// anchor selects between the TrafficMonitor-style default (flush against the
// LEFT edge of the notification area) and an offset from the taskbar's right
// edge, which is useful for machines whose tray is narrow.
func (w *widget) horizontalPos(m winui.TaskbarMetrics, width int32) int32 {
	offset := int32(w.feat.intOpt(optOffsetX, defaultOffsetX))

	if m.NotifyFound {
		// TrafficMonitor's look: the strip ends just before the tray, so its
		// right edge must not reach NotifyLeft. The extra gap keeps the widget
		// clear of the tray's 1-2px border and any rounded-corner padding, which
		// otherwise showed as a slight overlap.
		const trayGap = 2
		x := m.NotifyLeft - trayGap - width - offset
		if x < 0 {
			x = 0
		}
		return x
	}

	// No tray found: fall back to the taskbar's right edge.
	x := m.Width - width - offset
	if w.feat.boolOpt(optAvoidWidgets, defaultAvoidWidgets) && x < 0 {
		x = 0
	}
	return x
}

// sampleBackground reads the taskbar's real pixel colour beside the widget and
// caches it for paint.
//
// The probe point sits OUTSIDE the widget (just left of it, or just right when
// the widget is flush against the left edge). Sampling inside the widget's own
// rectangle would read the widget's pixels back — with a colour-key background
// that is the magenta key itself, which would then be mistaken for the taskbar
// colour and drive the text contrast the wrong way.
func (w *widget) sampleBackground(m winui.TaskbarMetrics, x, y, height int32) {
	tbScreen := winui.WindowRect(m.HWND)

	width := int32(widgetW)
	if f := int32(w.feat.intOpt(optWidth, defaultWidth)); f > 0 {
		width = f
	}

	// Prefer just left of the widget; fall back to just right of it.
	probeX := x - widgetProbeGap
	if probeX < 0 {
		probeX = x + width + widgetProbeGap
	}
	// Stay inside the taskbar so the sample is never taken from wallpaper.
	if probeX >= m.Width {
		probeX = m.Width - 1
	}
	screenX := tbScreen.Left + probeX
	screenY := tbScreen.Top + y + height/2

	if c, ok := winui.TaskbarBackground(screenX, screenY); ok {
		w.mu.Lock()
		w.bgColor = c
		w.hasBG = true
		w.mu.Unlock()
	}
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
	w.ensureAuxFonts()

	w.mu.Lock()
	s := w.stats
	sampledBG := w.bgColor
	hasBG := w.hasBG
	keyApplied := w.transparent
	w.mu.Unlock()

	// Transparent is only possible when the colour key was accepted; the key is
	// applied to the window regardless, so a "solid" request must paint over it
	// (a solid fill simply overwrites the key pixels).
	solid := w.feat.stringOpt(optBGMode, defaultBGMode) == "solid"
	transparent := keyApplied && !solid

	// Background: with a colour key accepted, fill with the key colour so the
	// TASKBAR's own pixels show through — the background is then genuinely
	// transparent (acrylic, gradients and accent changes all follow for free).
	// Only when the key could not be applied does an opaque colour get painted.
	if transparent {
		c.Fill(rect, widgetKeyColor)
	} else {
		bg := winui.RGB(32, 33, 36)
		if solid {
			bg = winui.RGBFromString(w.feat.stringOpt(optBGColor, defaultBGColor), bg)
		} else if hasBG {
			bg = sampledBG
		} else if accent, ok := winui.TaskbarColor(); ok {
			bg = accent
		}
		c.Fill(rect, bg)
	}

	inner := rect.Inset(widgetPad)

	fg := w.foreground(hasBG, sampledBG)

	// The grid layout is the default and the only one that lays out both columns;
	// the older single-line mode is kept for users who prefer one row.
	if w.feat.stringOpt(optLayout, defaultLayout) != "single-line" {
		w.paintGrid(c, s, inner, fg)
		return
	}

	restore := c.SelectFont(w.font)
	defer restore()
	parts := w.parts(s)
	if len(parts) == 0 {
		parts = []string{"GoBox"}
	}
	c.DrawText(strings.Join(parts, w.separator()), inner, fg,
		winui.DT_SINGLELINE|winui.DT_VCENTER|winui.DT_NOPREFIX|winui.DT_END_ELLIPSIS|w.alignFlags())
}

// Grid layout metrics, in device pixels.
const (
	// gridColGap is the fixed gutter between the system column and the network
	// column. A fixed gutter (rather than distributing free space) is what keeps
	// the two groups visually separate and their left edges stable.
	gridColGap = 14
	// gridLabelGap separates a label/icon from its value.
	gridLabelGap = 5
	// batteryIconW/H size the drawn battery glyph.
	batteryIconW = 26
	batteryIconH = 12
	// gridColumnMax caps each column so a wild network rate cannot push the
	// system column around; the excess is clipped, not reflowed.
	gridColumnMax = 150

	// gridNumFieldW is the fixed width the network number is right-aligned in.
	// This is what makes "13" and "276" share a right edge (the tabular-figures
	// effect) and keeps the units below each other on one x.
	gridNumFieldW = 46
	// gridUnitGap separates the number field from the unit.
	gridUnitGap = 4
	// gridUnitW is the room reserved for a unit ("B/s", "KB/s").
	gridUnitW = 40
	// unitWeight is how much of the foreground survives in the unit text.
	unitWeight = 0.6

	// gridLabelFieldW is the fixed field the left column's label or icon occupies,
	// so the values below each other start at one x.
	gridLabelFieldW = 30
	// gridSysValueW is the room reserved for a system value ("100%").
	gridSysValueW = 44
)

// paintGrid renders the readout as a 2x2 grid:
//
//	内存      80%        ↑ 13  KB/s
//	[电池]    100%       ↓ 276 KB/s
//
// The left column is system status, the right column is network traffic; the
// rows correspond (memory over battery, upload over download), the two columns
// are separated by a fixed gutter, and every row's content is left-aligned
// within its column so the icons, labels and arrows line up vertically.
func (w *widget) paintGrid(c *winui.Canvas, s Stats, inner winui.Rect, fg uint32) {
	leftCell, rightCell, rowTop, rowMid := w.gridCells(inner, c, s)

	// Row heights follow the font, so a large font stays legible instead of
	// overlapping the row below.
	rowH := rowMid - rowTop

	// Left column: memory (row 0) and battery (row 1).
	w.paintSystemRow(c, s, leftCell, rowTop, rowH, fg, true)
	w.paintSystemRow(c, s, leftCell, rowMid, rowH, fg, false)

	// Right column: upload then download, each on its own row and left-aligned.
	w.paintNetRow(c, s, rightCell, rowTop, rowH, fg, true)
	w.paintNetRow(c, s, rightCell, rowMid, rowH, fg, false)
}

// gridCells computes the two column rectangles and the two row positions.
//
// Column widths are measured from the actual content (so the gutter between the
// groups is exact), then capped: a runaway network rate must not push the system
// column sideways, which is the "fixed max width" the layout calls for.
func (w *widget) gridCells(inner winui.Rect, c *winui.Canvas, s Stats) (left, right winui.Rect, rowTop, rowMid int32) {
	leftW := w.systemColumnWidth(c, s)
	rightW := w.netColumnWidth(c, s)
	if leftW > gridColumnMax {
		leftW = gridColumnMax
	}
	if rightW > gridColumnMax {
		rightW = gridColumnMax
	}

	total := leftW + gridColGap + rightW
	// If the widget is narrower than the content, shrink the columns equally so
	// both groups stay visible rather than the right one falling off the edge.
	if total > inner.Width() {
		avail := inner.Width() - gridColGap
		if avail < 2 {
			avail = inner.Width()
		}
		leftW = avail / 2
		rightW = avail - leftW
		total = leftW + gridColGap + rightW
	}

	// Right-align the whole block when the configured alignment says so, which
	// keeps the widget hugging the clock on the right of the taskbar.
	var startX int32
	switch w.feat.stringOpt(optAlign, defaultAlign) {
	case "left":
		startX = inner.Left
	case "center":
		startX = inner.Left + (inner.Width()-total)/2
	default:
		startX = inner.Right - total
	}
	if startX < inner.Left {
		startX = inner.Left
	}

	left = winui.Rect{Left: startX, Top: inner.Top, Right: startX + leftW, Bottom: inner.Bottom}
	right = winui.Rect{Left: left.Right + gridColGap, Top: inner.Top, Right: left.Right + gridColGap + rightW, Bottom: inner.Bottom}

	// Two rows sized to the font, vertically centred as a block.
	_, lineH := c.MeasureText("Ag")
	if lineH <= 0 {
		lineH = inner.Height() / 2
	}
	if 2*lineH > inner.Height() {
		lineH = inner.Height() / 2
	}
	gap := int32(1)
	blockH := 2*lineH + gap
	top := inner.Top + (inner.Height()-blockH)/2
	if top < inner.Top {
		top = inner.Top
	}
	return left, right, top, top + lineH + gap
}

// systemColumnWidth is the left column's width: a fixed label field, the label
// gap, and the room reserved for a value. Using fixed fields (rather than each
// row's measured width) is what keeps the column from jittering as the numbers
// change.
func (w *widget) systemColumnWidth(c *winui.Canvas, s Stats) int32 {
	return gridLabelFieldW + gridLabelGap + gridSysValueW
}

// netColumnWidth measures the widest "arrow + number + unit" row on the right.
//
// The number field is fixed width (gridNumFieldW), so the measurement uses that
// constant rather than the value's own width — otherwise the column would resize
// every time a rate crossed a digit boundary.
func (w *widget) netColumnWidth(c *winui.Canvas, s Stats) int32 {
	if !w.feat.boolOpt(optShowUp, defaultShowUp) && !w.feat.boolOpt(optShowDown, defaultShowDown) {
		return 0
	}
	restore := c.SelectFont(w.auxFont)
	aw, _ := c.MeasureText("↑")
	uw, _ := c.MeasureText("KB/s")
	restore()
	return aw + gridLabelGap + gridNumFieldW + gridUnitGap + uw
}

// rowRect returns the rectangle of one grid row inside a column.
func rowRect(cell winui.Rect, top, rowH int32) winui.Rect {
	return winui.Rect{Left: cell.Left, Top: top, Right: cell.Right, Bottom: top + rowH}
}

// paintSystemRow draws the memory row (first=true) or the battery row.
func (w *widget) paintSystemRow(c *winui.Canvas, s Stats, cell winui.Rect, top, rowH int32, fg uint32, first bool) {
	r := rowRect(cell, top, rowH)
	labelFg := winui.BlendColors(fg, w.bgForBlend(), labelWeight)

	if first {
		if !w.feat.boolOpt(optShowMem, defaultShowMem) {
			return
		}
		w.drawLabelValue(c, "内存", pct(s.RAM), r, fg, labelFg)
		return
	}

	if !w.feat.boolOpt(optShowBattery, defaultShowBattery) || !s.BatteryPresent {
		// Without a battery the second row would be empty; show CPU there
		// instead so the grid does not look broken on desktops.
		if w.feat.boolOpt(optShowCPU, defaultShowCPU) {
			w.drawLabelValue(c, "CPU", pct(s.CPU), r, fg, labelFg)
		}
		return
	}
	w.drawBatteryRow(c, s, r, fg, labelFg)
}

// drawLabelValue draws "<label>    <value>", with the label de-emphasised and
// the value emphasised (bold, larger).
//
// The label occupies a fixed-width field and the value is left-aligned right
// after it, so the two rows' labels and values each start at the same x — the
// same alignment idea used in the network column.
func (w *widget) drawLabelValue(c *winui.Canvas, text, value string, r winui.Rect, fg, labelFg uint32) {
	restore := c.SelectFont(w.auxFont)
	// A fixed label field keeps "内存" and "CPU" from shifting their values.
	c.DrawText(text, winui.Rect{Left: r.Left, Top: r.Top, Right: r.Left + gridLabelFieldW, Bottom: r.Bottom},
		labelFg, winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
	restore()

	valueLeft := r.Left + gridLabelFieldW + gridLabelGap
	c.DrawText(value, winui.Rect{Left: valueLeft, Top: r.Top, Right: valueLeft + gridSysValueW, Bottom: r.Bottom},
		fg, winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
}

// drawBatteryRow draws the icon + percentage row, replacing the old "BAT" text.
//
// The icon occupies the same field width the text labels use, so the battery
// percentage lines up with the memory percentage above it.
func (w *widget) drawBatteryRow(c *winui.Canvas, s Stats, r winui.Rect, fg, labelFg uint32) {
	icon := winui.Rect{
		Left:   r.Left,
		Top:    r.Top + (r.Height()-batteryIconH)/2,
		Right:  r.Left + batteryIconW,
		Bottom: r.Top + (r.Height()-batteryIconH)/2 + batteryIconH,
	}
	drawBatteryIcon(c, icon, s.BatteryPercent, s.BatteryCharging, fg, labelFg)

	value := itoa(s.BatteryPercent) + "%"
	valueLeft := r.Left + gridLabelFieldW + gridLabelGap
	c.DrawText(value, winui.Rect{Left: valueLeft, Top: r.Top, Right: valueLeft + gridSysValueW, Bottom: r.Bottom},
		fg, winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
}

// paintNetRow draws the upload row (first=true) or the download row.
//
// Layout inside the row is: arrow, then the number right-aligned in a fixed
// field, then the unit left-aligned. The fixed number field is what produces the
// "tabular figures" effect the design calls for — "13" and "276" end on the same
// x, so the column reads as aligned even though the digit counts differ, and the
// units below each other start at the same x for free.
func (w *widget) paintNetRow(c *winui.Canvas, s Stats, cell winui.Rect, top, rowH int32, fg uint32, first bool) {
	r := rowRect(cell, top, rowH)

	var arrow string
	var bps float64
	if first {
		if !w.feat.boolOpt(optShowUp, defaultShowUp) {
			return
		}
		arrow, bps = "↑", s.NetUp
	} else {
		if !w.feat.boolOpt(optShowDown, defaultShowDown) {
			return
		}
		arrow, bps = "↓", s.NetDn
	}

	// Arrow in the auxiliary font so both arrows occupy identical width.
	switchTo := c.SelectFont(w.auxFont)
	aw, _ := c.MeasureText(arrow)
	c.DrawText(arrow, winui.Rect{Left: r.Left, Top: r.Top, Right: r.Left + aw + 2, Bottom: r.Bottom},
		fg, winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
	switchTo()

	value, unit := w.feat.rateParts(bps)

	// Right-align the number in a fixed-width field so digit counts do not shift
	// the unit to the left or right.
	numLeft := r.Left + aw + gridLabelGap
	numRight := numLeft + gridNumFieldW
	c.DrawText(value, winui.Rect{Left: numLeft, Top: r.Top, Right: numRight, Bottom: r.Bottom},
		fg, winui.DT_RIGHT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)

	if unit != "" {
		unitFg := winui.BlendColors(fg, w.bgForBlend(), unitWeight)
		restore := c.SelectFont(w.auxFont)
		ul := numRight + gridUnitGap
		c.DrawText(unit, winui.Rect{Left: ul, Top: r.Top, Right: ul + gridUnitW, Bottom: r.Bottom},
			unitFg, winui.DT_LEFT|winui.DT_VCENTER|winui.DT_SINGLELINE|winui.DT_NOPREFIX)
		restore()
	}
}

// annotationTextReturn is unused; labels are passed explicitly by callers.
//
// (Kept as documentation of the removed placeholder.)

// labelWeight is how much of the foreground survives in a de-emphasised label.
const labelWeight = 0.55

// foreground picks the text colour: the INVERSE of the taskbar background, so
// the readout always stands out instead of blending in.
//
// Dark text is chosen on a light taskbar and light text on a dark one, using
// the actual sampled background when available.
func (w *widget) foreground(hasBG bool, sampledBG uint32) uint32 {
	if !w.feat.boolOpt(optAutoFG, defaultAutoFG) {
		return winui.RGBFromString(w.feat.stringOpt(optFGColor, defaultFGColor), winui.RGB(255, 255, 255))
	}

	// The contrast base is the taskbar colour in both modes: with a transparent
	// background the text sits directly on the taskbar, and with an opaque one
	// the widget is painted in the sampled taskbar colour anyway. sampledBG is
	// the caller's snapshot of the same value, so it is preferred (it stays
	// correct even if the sample was refreshed mid-paint).
	base := sampledBG
	if !hasBG {
		// No usable sample: fall back to the system light/dark hint.
		if winui.DarkModeEnabled() {
			return winui.RGB(255, 255, 255)
		}
		return winui.RGB(0, 0, 0)
	}
	return winui.ContrastText(base)
}

// bgForBlend returns the colour that de-emphasised text is blended toward.
//
// With a transparent background the text sits on the taskbar, so it blends
// toward the sampled taskbar colour; otherwise it blends toward whatever was
// painted (the configured solid colour).
func (w *widget) bgForBlend() uint32 {
	if w.transparent {
		w.mu.Lock()
		bg := w.bgColor
		has := w.hasBG
		w.mu.Unlock()
		if has {
			return bg
		}
		if winui.DarkModeEnabled() {
			return winui.RGB(0, 0, 0)
		}
		return winui.RGB(255, 255, 255)
	}
	return winui.RGBFromString(w.feat.stringOpt(optBGColor, defaultBGColor), winui.RGB(32, 33, 36))
}

// ensureAuxFonts creates the secondary fonts used for labels, arrows and units.
//
// They are two points smaller than the value font and not bold, which is what
// gives the readout its hierarchy: the numbers stand out, the labels and units
// recede, without a second colour that would fight the auto-contrast rule.
func (w *widget) ensureAuxFonts() {
	face := w.feat.stringOpt(optFontFamily, defaultFontFamily)
	size := int32(w.feat.intOpt(optFontSize, defaultFontSize)) - 2
	if size < 6 {
		size = 6
	}
	key := face + "|aux|" + itoa(int(size))

	w.mu.Lock()
	if w.auxFont != 0 && w.auxFontKey == key {
		w.mu.Unlock()
		return
	}
	old := w.auxFont
	if w.transparent {
		w.auxFont = winui.NewFontQuality(face, size, winui.FW_NORMAL, winui.NONANTIALIASED_QUAL)
	} else {
		w.auxFont = winui.NewFont(face, size, winui.FW_NORMAL)
	}
	w.auxFontKey = key
	w.mu.Unlock()

	if old != 0 {
		winui.DeleteObject(old)
	}
}

// drawBatteryIcon draws a battery outline with a fill proportional to percent,
// plus a lightning bolt when charging.
//
// A drawn glyph replaces the old "BAT" text: it reads at a glance and, because
// it is vector-drawn, it follows the text colour on light and dark taskbars
// alike (an emoji or bitmap would not).
func drawBatteryIcon(c *winui.Canvas, r winui.Rect, percent int, charging bool, fg, dim uint32) {
	if r.Width() < 4 || r.Height() < 4 {
		return
	}
	// Body plus a small terminal nub on the right.
	body := r
	body.Right -= r.Width() / 8
	nubW := r.Width() / 8
	if nubW < 2 {
		nubW = 2
	}
	nubH := r.Height() / 2
	if nubH < 2 {
		nubH = 2
	}
	nub := winui.Rect{
		Left:   body.Right,
		Top:    r.Top + (r.Height()-nubH)/2,
		Right:  body.Right + nubW,
		Bottom: r.Top + (r.Height()-nubH)/2 + nubH,
	}

	// Outline (dim) and nub, then the proportional fill.
	c.StrokeRect(body, dim, 1)
	c.Fill(nub, dim)

	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	inner := body.Inset(2)
	if inner.Width() > 0 && inner.Height() > 0 && percent > 0 {
		fillW := int32(float64(inner.Width()) * float64(percent) / 100.0)
		if fillW < 1 {
			fillW = 1
		}
		c.Fill(winui.Rect{Left: inner.Left, Top: inner.Top, Right: inner.Left + fillW, Bottom: inner.Bottom}, fg)
	}

	if charging {
		// A small bolt centred on the body, in the background colour so it stays
		// visible over the fill.
		cx := (body.Left + body.Right) / 2
		cy := (body.Top + body.Bottom) / 2
		h := body.Height() / 2
		if h < 3 {
			h = 3
		}
		pts := []winui.POINT{
			{X: cx - h/3, Y: cy - h/2},
			{X: cx + h/4, Y: cy - h/6},
			{X: cx - h/8, Y: cy - h/8},
			{X: cx + h/3, Y: cy + h/2},
			{X: cx - h/5, Y: cy + h/8},
			{X: cx + h/8, Y: cy + h/8},
		}
		c.FillPolygon(pts, dim)
	}
}

// alignFlags maps the configured alignment to its DT_* flag.
func (w *widget) alignFlags() uint32 {
	switch w.feat.stringOpt(optAlign, defaultAlign) {
	case "left":
		return winui.DT_LEFT
	case "center":
		return winui.DT_CENTER
	default:
		return winui.DT_RIGHT
	}
}

// parts assembles the readout fields from the enabled options, in display
// order. Returning the fields (rather than a joined string) is what lets the
// two-line layout split on real boundaries instead of guessing at separators.
func (w *widget) parts(s Stats) []string {
	parts := make([]string, 0, 7)
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
	if w.feat.boolOpt(optShowBattery, defaultShowBattery) && s.BatteryPresent {
		batt := "BAT " + itoa(s.BatteryPercent) + "%"
		if s.BatteryCharging {
			batt += "+" // plugged in
		}
		parts = append(parts, batt)
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
	return parts
}

// text assembles the single-line readout from the enabled fields.
func (w *widget) text(s Stats) string {
	return strings.Join(w.parts(s), w.separator())
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
	// Bold: the readout sits directly on the taskbar with no background panel,
	// so a heavier weight is what keeps it legible against busy wallpaper or a
	// translucent taskbar.
	weight := int32(winui.FW_BOLD)
	key := face + "|" + itoa(int(size)) + "|" + itoa(int(weight))

	w.mu.Lock()
	if w.font != 0 && w.fontKey == key {
		w.mu.Unlock()
		return
	}
	old := w.font
	// Non-antialiased when the colour key is active: ClearType's blended edges
	// are not the exact key colour and would leave magenta fringes.
	if w.transparent {
		w.font = winui.NewFontQuality(face, size, weight, winui.NONANTIALIASED_QUAL)
	} else {
		w.font = winui.NewFont(face, size, weight)
	}
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

// Stop closes the widget window and waits for it to actually disappear.
//
// The close is requested with WM_CLOSE rather than DestroyWindow because
// DestroyWindow must run on the window's owning thread; Stop is called from the
// module's goroutine, so a direct DestroyWindow silently failed and left the
// window alive. rebuildWidget() then created a replacement on top of it, which
// is how three overlapping GoBoxTaskbar windows piled up at the same spot.
//
// The bounded wait makes the destroy/recreate sequence deterministic. It is
// idempotent.
func (w *widget) Stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	win := w.win
	w.mu.Unlock()

	var hwnd winui.HWND
	if win != nil {
		hwnd = win.HWND()
		// Post the close request; the widget's own message loop performs the
		// actual DestroyWindow on its thread.
		winui.PostMessage(hwnd, winui.WM_CLOSE, 0, 0)
	}

	// Poll until the handle is gone, bounded so a stuck loop cannot hang Stop.
	if hwnd.Valid() {
		deadline := time.Now().Add(widgetDestroyTimeout)
		for time.Now().Before(deadline) {
			if !winui.IsWindow(hwnd) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
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
