//go:build windows

package winui

import (
	"image"
	"runtime"
	"syscall"
	"unsafe"
)

// runtimeKeepAlive keeps a buffer reachable until the GDI call returns.
func runtimeKeepAlive(v any) { runtime.KeepAlive(v) }

// Additional GDI procedures needed for on-screen drawing. They live in this
// file rather than api_windows.go so the drawing surface has a single home.
var (
	procCreatePen              = gdi32.NewProc("CreatePen")
	procCreateDIBSection       = gdi32.NewProc("CreateDIBSection")
	procStretchDIBits          = gdi32.NewProc("StretchDIBits")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procRectangle              = gdi32.NewProc("Rectangle")
	procMoveToEx               = gdi32.NewProc("MoveToEx")
	procLineTo                 = gdi32.NewProc("LineTo")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procGetSysColorBrush       = user32.NewProc("GetSysColorBrush")
	procSetPixelV              = gdi32.NewProc("SetPixelV")
	procPatBlt                 = gdi32.NewProc("PatBlt")
)

// GDI pen/brush/ROP constants used by the drawing helpers.
const (
	PS_SOLID = 0
	PS_DOT   = 2

	PS_NULL_BRUSH   = 5
	NULL_BRUSH_MODE = 5
	HOLLOW_BRUSH_ID = 5

	SRCCOPY = 0x00CC0020

	BI_RGB         = 0
	DIB_RGB_COLORS = 0

	COLOR_WINDOW = 5
)

// Font weights.
const (
	FW_MEDIUM  = 500
	FW_BOLD_DW = 700
)

// RGB builds a COLORREF from 8-bit components (COLORREF is 0x00BBGGRR).
func RGB(r, g, b byte) uint32 {
	return uint32(r) | uint32(g)<<8 | uint32(b)<<16
}

// RGBFromString parses "#RRGGBB" / "#RGB" and falls back to fallback.
func RGBFromString(s string, fallback uint32) uint32 {
	hex := func(c byte) (byte, bool) {
		switch {
		case c >= '0' && c <= '9':
			return c - '0', true
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10, true
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10, true
		}
		return 0, false
	}
	if len(s) == 0 || s[0] != '#' {
		return fallback
	}
	s = s[1:]
	switch len(s) {
	case 3:
		r, ok1 := hex(s[0])
		g, ok2 := hex(s[1])
		b, ok3 := hex(s[2])
		if !ok1 || !ok2 || !ok3 {
			return fallback
		}
		return RGB(r*17, g*17, b*17)
	case 6:
		rh, ok1 := hex(s[0])
		rl, ok2 := hex(s[1])
		gh, ok3 := hex(s[2])
		gl, ok4 := hex(s[3])
		bh, ok5 := hex(s[4])
		bl, ok6 := hex(s[5])
		if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 {
			return fallback
		}
		return RGB(rh<<4|rl, gh<<4|gl, bh<<4|bl)
	}
	return fallback
}

// Common dark/light palette colors shared by the native panels.
var (
	ColorPanelBG  = RGB(32, 33, 36)
	ColorPanelFG  = RGB(232, 234, 237)
	ColorPanelDim = RGB(154, 160, 166)
	ColorAccent   = RGB(78, 158, 255)
	ColorSuccess  = RGB(120, 210, 140)
	ColorDanger   = RGB(255, 120, 120)
	ColorRowAlt   = RGB(42, 44, 48)
	ColorRowSel   = RGB(58, 90, 140)
)

// Canvas is a thin drawing surface over a Win32 device context.
//
// It exists so modules can paint without touching GDI directly, keeping the
// syscall plumbing in one place (and cgo out of the build, per PRD GFR-11).
type Canvas struct {
	hdc uintptr
}

// BeginPaint starts a paint cycle for hwnd and returns the canvas plus the
// PAINTSTRUCT that must be passed back to EndPaint.
func BeginPaint(hwnd HWND) (*Canvas, *PAINTSTRUCT) {
	ps := &PAINTSTRUCT{}
	hdc, _, _ := procBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(ps)))
	return &Canvas{hdc: hdc}, ps
}

// EndPaint finishes a paint cycle opened by BeginPaint.
func EndPaint(hwnd HWND, ps *PAINTSTRUCT) {
	if ps == nil {
		return
	}
	procEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(ps)))
}

// DC returns the raw device context handle (for advanced GDI use).
func (c *Canvas) DC() uintptr { return c.hdc }

// NewFont creates a font handle; the caller must call DeleteObject when done.
func NewFont(face string, sizePt int32, weight int32) uintptr {
	if sizePt <= 0 {
		sizePt = 9
	}
	if weight == 0 {
		weight = FW_NORMAL
	}
	// Negative height requests a character height (not cell height).
	lf := LOGFONTW{
		Height:  -sizePt * 96 / 72,
		Weight:  weight,
		CharSet: DEFAULT_CHARSET,
		Quality: CLEARTYPE_QUALITY,
	}
	copy(lf.FaceName[:], syscall.StringToUTF16(face))
	r, _, _ := procCreateFontIndirectW.Call(uintptr(unsafe.Pointer(&lf)))
	return r
}

// DeleteObject releases a GDI object (font, pen, brush, bitmap).
func DeleteObject(h uintptr) { procDeleteObject.Call(h) }

// SelectFont installs a font and returns a restore function.
func (c *Canvas) SelectFont(hfont uintptr) func() {
	if hfont == 0 {
		return func() {}
	}
	old, _, _ := procSelectObject.Call(c.hdc, hfont)
	return func() { procSelectObject.Call(c.hdc, old) }
}

// Fill paints a solid rectangle.
func (c *Canvas) Fill(r Rect, color uint32) {
	brush, _, _ := procCreateSolidBrush.Call(uintptr(color))
	if brush == 0 {
		return
	}
	defer procDeleteObject.Call(brush)
	procFillRect.Call(c.hdc, uintptr(unsafe.Pointer(&r)), brush)
}

// FillRound paints a filled rectangle with a 1px border of borderColor.
func (c *Canvas) FillRound(r Rect, fill uint32, border uint32) {
	c.Fill(r, fill)
	if border != 0 {
		c.StrokeRect(r, border, 1)
	}
}

// StrokeRect draws a rectangle outline.
func (c *Canvas) StrokeRect(r Rect, color uint32, width int32) {
	if width <= 0 {
		width = 1
	}
	pen, _, _ := procCreatePen.Call(PS_SOLID, uintptr(width), uintptr(color))
	if pen == 0 {
		return
	}
	defer procDeleteObject.Call(pen)
	old, _, _ := procSelectObject.Call(c.hdc, pen)
	defer procSelectObject.Call(c.hdc, old)
	// A NULL brush keeps the interior untouched.
	nullBrush, _, _ := procGetStockObject.Call(5)
	if nullBrush != 0 {
		oldB, _, _ := procSelectObject.Call(c.hdc, nullBrush)
		defer procSelectObject.Call(c.hdc, oldB)
	}
	procRectangle.Call(c.hdc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom))
}

// Line draws a straight line.
func (c *Canvas) Line(x1, y1, x2, y2 int32, color uint32, width int32) {
	if width <= 0 {
		width = 1
	}
	pen, _, _ := procCreatePen.Call(PS_SOLID, uintptr(width), uintptr(color))
	if pen == 0 {
		return
	}
	defer procDeleteObject.Call(pen)
	old, _, _ := procSelectObject.Call(c.hdc, pen)
	defer procSelectObject.Call(c.hdc, old)
	procMoveToEx.Call(c.hdc, uintptr(x1), uintptr(y1), 0)
	procLineTo.Call(c.hdc, uintptr(x2), uintptr(y2))
}

// Text draws a single line of text with a transparent background.
func (c *Canvas) Text(s string, x, y int32, color uint32) {
	if s == "" {
		return
	}
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return
	}
	procSetBkMode.Call(c.hdc, TRANSPARENT)
	procSetTextColor.Call(c.hdc, uintptr(color))
	procTextOutW.Call(c.hdc, uintptr(x), uintptr(y),
		uintptr(unsafe.Pointer(p)), uintptr(len(syscall.StringToUTF16(s))-1))
}

// DrawText renders text inside r using the given DT_* format flags.
func (c *Canvas) DrawText(s string, r Rect, color uint32, flags uint32) {
	if s == "" {
		return
	}
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return
	}
	procSetBkMode.Call(c.hdc, TRANSPARENT)
	procSetTextColor.Call(c.hdc, uintptr(color))
	rr := r
	procDrawTextW.Call(c.hdc, uintptr(unsafe.Pointer(p)), ^uintptr(0),
		uintptr(unsafe.Pointer(&rr)), uintptr(flags))
}

// MeasureText returns the width and height of s under the current font.
func (c *Canvas) MeasureText(s string) (int32, int32) {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return 0, 0
	}
	var sz struct{ CX, CY int32 }
	n := len(syscall.StringToUTF16(s)) - 1
	procGetTextExtentPoint32W.Call(c.hdc, uintptr(unsafe.Pointer(p)), uintptr(n),
		uintptr(unsafe.Pointer(&sz)))
	return sz.CX, sz.CY
}

// Image blits an image.Image into dest using StretchDIBits.
//
// The source is converted to 32-bit BGRA, which is the layout Windows GDI
// expects for a top-down BI_RGB DIB.
func (c *Canvas) Image(dest Rect, img image.Image) {
	if img == nil {
		return
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return
	}
	buf := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			o := (y*w + x) * 4
			// Pre-multiply against the panel background for opaque rendering.
			buf[o+0] = byte(bl >> 8)
			buf[o+1] = byte(g >> 8)
			buf[o+2] = byte(r >> 8)
			buf[o+3] = byte(a >> 8)
		}
	}
	dw := dest.Width()
	dh := dest.Height()
	procStretchDIBits.Call(
		c.hdc,
		uintptr(dest.Left), uintptr(dest.Top), uintptr(dw), uintptr(dh),
		0, 0, uintptr(w), uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&bitmapInfo{
			Size:        uint32(unsafe.Sizeof(bitmapInfo{})),
			Width:       int32(w),
			Height:      -int32(h), // negative => top-down
			Planes:      1,
			BitCount:    32,
			Compression: BI_RGB,
		})),
		DIB_RGB_COLORS, SRCCOPY,
	)
	runtimeKeepAlive(buf)
}

// bitmapInfo mirrors the Win32 BITMAPINFOHEADER.
type bitmapInfo struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// DrawWindowText is a convenience wrapper for WM_PAINT handlers that just want
// to run a drawing function and have BeginPaint/EndPaint managed for them.
func DrawWindowText(hwnd HWND, fn func(c *Canvas)) {
	c, ps := BeginPaint(hwnd)
	if c.hdc == 0 {
		return
	}
	fn(c)
	EndPaint(hwnd, ps)
}
