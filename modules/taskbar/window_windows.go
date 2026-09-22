//go:build windows

package statusbar

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/gonutz/w32"
)

// window is a small Win32 taskbar widget that renders live CPU/RAM/Net stats,
// mirroring the TrafficMonitor display style.
type window struct {
	hwnd     w32.HWND
	hInst    w32.HINSTANCE
	font     w32.HFONT
	bgBrush  w32.HBRUSH
	mu       sync.Mutex
	current  Stats
	showCPU  bool
	showRAM  bool
	showNet  bool
	updateMs int
}

var (
	regMu   sync.Mutex
	reg     = map[w32.HWND]*window{}
	classID uint32
)

// rgb builds a COLORREF from RGB components.
func rgb(r, g, b byte) w32.COLORREF {
	return w32.COLORREF(uint32(r) | uint32(g)<<8 | uint32(b)<<16)
}

// SetStats updates the displayed snapshot (called from the collector goroutine).
func (w *window) SetStats(s Stats) {
	w.mu.Lock()
	w.current = s
	w.mu.Unlock()
}

func (w *window) className() string {
	regMu.Lock()
	classID++
	name := fmt.Sprintf("PCMannagerStatusbar_%d", classID)
	regMu.Unlock()
	return name
}

// Run creates the window and runs the message loop until WM_DESTROY is posted.
func (w *window) Run() {
	className := w.className()
	utf16Name, _ := syscall.UTF16FromString(className)

	w.hInst = w32.GetModuleHandle("")
	w.bgBrush = w32.CreateSolidBrush(uint32(rgb(28, 28, 30)))
	wndClass := w32.WNDCLASSEX{
		Size:      uint32(unsafe.Sizeof(w32.WNDCLASSEX{})),
		Style:     w32.CS_HREDRAW | w32.CS_VREDRAW,
		WndProc:   syscall.NewCallback(wndProc),
		Instance:  w.hInst,
		Cursor:    w32.LoadCursor(0, w32.MakeIntResource(w32.IDC_ARROW)),
		Background: w.bgBrush,
		ClassName: &utf16Name[0],
	}
	w32.RegisterClassEx(&wndClass)

	sw, sh := w32.GetSystemMetrics(w32.SM_CXSCREEN), w32.GetSystemMetrics(w32.SM_CYSCREEN)
	const width, height = 200, 22
	x := sw - width - 3
	y := sh - height - 3

	w.hwnd = w32.CreateWindowExStr(
		w32.WS_EX_TOPMOST|w32.WS_EX_TOOLWINDOW,
		className, "",
		w32.WS_POPUP|w32.WS_VISIBLE,
		x, y, width, height,
		0, 0, w.hInst, nil,
	)
	if w.hwnd == 0 {
		w32.DeleteObject(w32.HGDIOBJ(w.bgBrush))
		return
	}
	regMu.Lock()
	reg[w.hwnd] = w
	regMu.Unlock()

	w.font = w32.CreateFontIndirect(&w32.LOGFONT{
		Height:         -13,
		Width:          0,
		Weight:         400,
		Quality:        w32.DEFAULT_QUALITY,
		PitchAndFamily: w32.DEFAULT_PITCH | w32.FF_DONTCARE,
	})

	if w.updateMs < 250 {
		w.updateMs = 250
	}
	w32.SetTimer(w.hwnd, 1, uint(w.updateMs), 0)

	msg := &w32.MSG{}
	for w32.GetMessage(msg, 0, 0, 0) != 0 {
		w32.TranslateMessage(msg)
		w32.DispatchMessage(msg)
	}
}

func wndProc(hwnd w32.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	regMu.Lock()
	w := reg[hwnd]
	regMu.Unlock()
	switch msg {
	case w32.WM_PAINT:
		if w != nil {
			w.paint()
		}
		return w32.DefWindowProc(hwnd, msg, wParam, lParam)
	case w32.WM_TIMER:
		w32.InvalidateRect(hwnd, nil, true)
		return 0
	case w32.WM_DESTROY:
		regMu.Lock()
		delete(reg, hwnd)
		regMu.Unlock()
		w32.PostQuitMessage(0)
		return 0
	}
	return w32.DefWindowProc(hwnd, msg, wParam, lParam)
}

func (w *window) paint() {
	var ps w32.PAINTSTRUCT
	hdc := w32.BeginPaint(w.hwnd, &ps)
	defer w32.EndPaint(w.hwnd, &ps)

	w32.SetBkMode(hdc, w32.TRANSPARENT)
	w.mu.Lock()
	s := w.current
	w.mu.Unlock()

	old := w32.SelectObject(hdc, w32.HGDIOBJ(w.font))
	defer w32.SelectObject(hdc, old)

	x := 6
	if w.showCPU {
		w32.SetTextColor(hdc, rgb(120, 200, 255))
		w32.TextOut(hdc, x, 3, fmt.Sprintf("CPU %.0f%%", s.CPU))
		x += 64
	}
	if w.showRAM {
		w32.SetTextColor(hdc, rgb(255, 180, 120))
		w32.TextOut(hdc, x, 3, fmt.Sprintf("MEM %.0f%%", s.RAM))
		x += 76
	}
	if w.showNet {
		w32.SetTextColor(hdc, rgb(150, 255, 150))
		w32.TextOut(hdc, x, 3, fmt.Sprintf("↑%s ↓%s", humanRate(s.NetUp), humanRate(s.NetDn)))
	}
}

// Stop posts WM_DESTROY to close the window.
func (w *window) Stop() {
	if w.hwnd != 0 {
		w32.PostMessage(w.hwnd, w32.WM_DESTROY, 0, 0)
	}
}

// newWindow builds a taskbar window from the active config.
func newWindow(cfg *statusbarCfg) *window {
	return &window{
		showCPU:  cfg.ShowCPU,
		showRAM:  cfg.ShowRAM,
		showNet:  cfg.ShowNet,
		updateMs: cfg.UpdateMs,
	}
}
