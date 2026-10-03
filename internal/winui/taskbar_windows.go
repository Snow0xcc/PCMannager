//go:build windows

package winui

// TaskbarMetrics describes the taskbar geometry needed to embed a widget in the
// TrafficMonitor style.
type TaskbarMetrics struct {
	// HWND is the Shell_TrayWnd handle (0 when the taskbar is unavailable).
	HWND HWND
	// Width/Height are the taskbar's size in pixels.
	Width, Height int32
	// NotifyLeft is the LEFT edge of the notification area (TrayNotifyWnd, which
	// holds the clock and tray icons), in taskbar client coordinates.
	//
	// TrafficMonitor sits immediately to the left of this, flush against the
	// clock. Anchoring to the taskbar's right edge instead (width-offset) lands
	// the widget inside the clock whenever the tray is wide, which is the "wrong
	// position" this replaced.
	NotifyLeft int32
	// NotifyFound reports whether TrayNotifyWnd was located. When false, callers
	// fall back to the taskbar's right edge.
	NotifyFound bool
}

// TaskbarMetricsNow measures the primary taskbar.
//
// Everything is resolved live rather than cached: Explorer restarting replaces
// both the taskbar window and its children.
func TaskbarMetricsNow() TaskbarMetrics {
	m := TaskbarMetrics{}
	tb := FindTaskbar()
	if !tb.Valid() {
		return m
	}
	m.HWND = tb

	client := ClientRect(tb)
	m.Width = client.Width()
	m.Height = client.Height()

	// TrayNotifyWnd is a direct child of Shell_TrayWnd. Its rectangle is in
	// screen coordinates, so the taskbar's own screen origin is subtracted to
	// get a client-relative x.
	notify := FindWindowEx(tb, Invalid, "TrayNotifyWnd", "")
	if !notify.Valid() {
		return m
	}
	tbScreen := WindowRect(tb)
	notifyScreen := WindowRect(notify)
	m.NotifyLeft = notifyScreen.Left - tbScreen.Left
	m.NotifyFound = true
	return m
}

// TaskbarBackground samples the taskbar's actual pixel colour near x.
//
// This is deliberately NOT DwmGetColorizationColor: that returns the accent
// colour, which can differ completely from what the taskbar is painted with
// (measured here: accent was orange while a light-theme taskbar was solid
// white). Using the accent made the widget clash with the taskbar it lives in.
//
// It returns false when the pixel cannot be read, so callers keep their own
// fallback rather than trusting a garbage colour.
func TaskbarBackground(x, y int32) (uint32, bool) {
	dc, _, _ := procGetDC.Call(0)
	if dc == 0 {
		return 0, false
	}
	defer procReleaseDC.Call(0, dc)

	px, _, _ := procGetPixel.Call(dc, uintptr(x), uintptr(y))
	c := uint32(px)
	// CLR_INVALID (0xFFFFFFFF) means the read failed.
	if c == 0xFFFFFFFF {
		return 0, false
	}
	return c, true
}
