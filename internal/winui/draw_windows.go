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
	procStretchBlt             = gdi32.NewProc("StretchBlt")
	procSetStretchBltMode      = gdi32.NewProc("SetStretchBltMode")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procRectangle              = gdi32.NewProc("Rectangle")
	procMoveToEx               = gdi32.NewProc("MoveToEx")
	procLineTo                 = gdi32.NewProc("LineTo")
	procEllipse                = gdi32.NewProc("Ellipse")
	procPolygon                = gdi32.NewProc("Polygon")
	procPolyline               = gdi32.NewProc("Polyline")
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

	// HALFTONE makes StretchBlt average pixels instead of dropping them, so a
	// downscaled screenshot does not alias.
	HALFTONE = 4

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
//
// It uses ClearType, which is right for ordinary windows on an opaque
// background.
func NewFont(face string, sizePt int32, weight int32) uintptr {
	return NewFontQuality(face, sizePt, weight, CLEARTYPE_QUALITY)
}

// NewFontQuality creates a font with an explicit GDI quality setting.
//
// A colour-key (transparent) window must pass NONANTIALIASED_QUAL, because
// ClearType's subpixel fringes are blends against the background rather than
// the exact key colour: they are not keyed out, so every glyph ends up ringed
// in magenta. Hard-edged glyphs are either key (transparent) or text colour
// (opaque), which is exactly what a transparent background needs.
func NewFontQuality(face string, sizePt int32, weight int32, quality byte) uintptr {
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
		Quality: quality,
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

// StrokeEllipse draws an ellipse outline inscribed in r.
func (c *Canvas) StrokeEllipse(r Rect, color uint32, width int32) {
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
	nullBrush, _, _ := procGetStockObject.Call(HOLLOW_BRUSH_ID)
	if nullBrush != 0 {
		oldB, _, _ := procSelectObject.Call(c.hdc, nullBrush)
		defer procSelectObject.Call(c.hdc, oldB)
	}
	procEllipse.Call(c.hdc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom))
}

// StrokePolyline draws connected line segments through pts.
//
// The points are converted from the caller's slice into a flat POINT array
// because GDI takes a contiguous array, not a Go slice of structs with padding
// assumptions.
func (c *Canvas) StrokePolyline(pts []POINT, color uint32, width int32) {
	if len(pts) < 2 {
		return
	}
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

	buffer := make([]POINT, len(pts))
	copy(buffer, pts)
	procPolyline.Call(c.hdc, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	runtimeKeepAlive(buffer)
}

// FillPolygon paints a closed filled polygon.
func (c *Canvas) FillPolygon(pts []POINT, color uint32) {
	if len(pts) < 3 {
		return
	}
	brush, _, _ := procCreateSolidBrush.Call(uintptr(color))
	if brush == 0 {
		return
	}
	defer procDeleteObject.Call(brush)
	old, _, _ := procSelectObject.Call(c.hdc, brush)
	defer procSelectObject.Call(c.hdc, old)

	buffer := make([]POINT, len(pts))
	copy(buffer, pts)
	procPolygon.Call(c.hdc, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	runtimeKeepAlive(buffer)
}

// Inset shrinks r by d on every side.
func (r Rect) Inset(d int32) Rect {
	return Rect{Left: r.Left + d, Top: r.Top + d, Right: r.Right - d, Bottom: r.Bottom - d}
}

// BlendColors mixes fg toward bg; weight is how much of fg survives (0..1).
//
// It is how the widget derives a de-emphasised variant of the text colour: a
// label or unit rendered at ~0.55 reads as secondary without introducing a
// second hardcoded colour that would break the auto-contrast rule.
func BlendColors(fg, bg uint32, weight float64) uint32 {
	if weight < 0 {
		weight = 0
	}
	if weight > 1 {
		weight = 1
	}
	// COLORREF is 0x00BBGGRR: low byte red, high byte blue.
	mix := func(f, b uint32) byte {
		v := float64(f)*weight + float64(b)*(1-weight)
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		return byte(v + 0.5)
	}
	return RGB(
		mix(fg&0xFF, bg&0xFF),
		mix((fg>>8)&0xFF, (bg>>8)&0xFF),
		mix((fg>>16)&0xFF, (bg>>16)&0xFF),
	)
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

// Image blits an image.Image into dest.
//
// The source is loaded into a DIB section and transferred with BitBlt
// (StretchBlt when dest has a different size), rather than handing
// StretchDIBits a Go-heap buffer.
//
// Why the DIB section matters: StretchDIBits silently fails (returns 0 and
// paints nothing) when its source buffer is large AND lives on the Go heap —
// measured failure from 768x768 up, success at 512x512 — which made the
// screenshot editor render a full-screen capture as pure black. The same
// buffer handed over in GlobalAlloc memory blitted fine, so the fix is to let
// Windows own the pixels. A DIB section also avoids the extra copy that a
// separate bitmap would need.
func (c *Canvas) Image(dest Rect, img image.Image) {
	if img == nil || c.hdc == 0 {
		return
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || dest.Width() <= 0 || dest.Height() <= 0 {
		return
	}

	srcDC, bits, release, err := newDIBSection(c.hdc, w, h)
	if err != nil {
		return
	}
	defer release()

	// Fill the DIB section's own memory directly: 32-bit BGRA, top-down.
	pix := unsafe.Slice((*byte)(bits), w*h*4)
	for y := 0; y < h; y++ {
		row := y * w * 4
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			o := row + x*4
			pix[o+0] = byte(bl >> 8)
			pix[o+1] = byte(g >> 8)
			pix[o+2] = byte(r >> 8)
			pix[o+3] = 0xFF
		}
	}

	dw, dh := dest.Width(), dest.Height()
	if dw == int32(w) && dh == int32(h) {
		procBitBlt.Call(c.hdc, uintptr(dest.Left), uintptr(dest.Top),
			uintptr(dw), uintptr(dh),
			srcDC, 0, 0, SRCCOPY)
		return
	}

	// Scaling: HALFTONE keeps screenshots from looking jagged when they are
	// fitted to a different window size.
	procSetStretchBltMode.Call(c.hdc, HALFTONE)
	procStretchBlt.Call(c.hdc, uintptr(dest.Left), uintptr(dest.Top),
		uintptr(dw), uintptr(dh),
		srcDC, 0, 0, uintptr(w), uintptr(h), SRCCOPY)
}

// ImageSubRect blits a part of img into dest.
//
// src is the region of img to take (in image coordinates); dest is where it
// lands. The screenshot editor uses this to redraw only the selection at full
// brightness while the rest of the surface stays dimmed.
func (c *Canvas) ImageSubRect(dest, src Rect, img image.Image) {
	if img == nil || c.hdc == 0 {
		return
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || dest.Width() <= 0 || dest.Height() <= 0 {
		return
	}
	if src.Width() <= 0 || src.Height() <= 0 {
		return
	}

	srcDC, bits, release, err := newDIBSection(c.hdc, b.Dx(), b.Dy())
	if err != nil {
		return
	}
	defer release()

	pix := unsafe.Slice((*byte)(bits), b.Dx()*b.Dy()*4)
	for y := 0; y < b.Dy(); y++ {
		row := y * b.Dx() * 4
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			o := row + x*4
			pix[o+0] = byte(bl >> 8)
			pix[o+1] = byte(g >> 8)
			pix[o+2] = byte(r >> 8)
			pix[o+3] = 0xFF
		}
	}

	procSetStretchBltMode.Call(c.hdc, HALFTONE)
	procStretchBlt.Call(c.hdc, uintptr(dest.Left), uintptr(dest.Top),
		uintptr(dest.Width()), uintptr(dest.Height()),
		srcDC, uintptr(src.Left), uintptr(src.Top),
		uintptr(src.Width()), uintptr(src.Height()), SRCCOPY)
}

// FillAlpha paints r with color at the given opacity (0-255) using AlphaBlend,
// so the screenshot editor can dim the screen without hiding it.
func (c *Canvas) FillAlpha(r Rect, color uint32, alpha byte) {
	if c.hdc == 0 || r.Width() <= 0 || r.Height() <= 0 || alpha == 0 {
		return
	}

	// A 1x1 source surface holding the colour, stretched over r.
	srcDC, bits, release, err := newDIBSection(c.hdc, 1, 1)
	if err != nil {
		return
	}
	defer release()

	pix := unsafe.Slice((*byte)(bits), 4)
	pix[0] = byte(color & 0xFF)         // B
	pix[1] = byte((color >> 8) & 0xFF)  // G
	pix[2] = byte((color >> 16) & 0xFF) // R
	pix[3] = 0xFF

	// BLENDFUNCTION is four bytes and is passed BY VALUE in the x64 calling
	// convention, packed into one register — not by pointer. Passing
	// &blend made AlphaBlend fail silently (returned 0, painted nothing), so
	// the editor's dim overlay never appeared.
	blend := uintptr(acSrcOver) |
		uintptr(0)<<8 |
		uintptr(alpha)<<16 |
		uintptr(0)<<24
	procAlphaBlend.Call(
		c.hdc,
		uintptr(r.Left), uintptr(r.Top), uintptr(r.Width()), uintptr(r.Height()),
		srcDC, 0, 0, 1, 1,
		blend,
		AC_SRC_OVER,
	)
}

// AlphaBlend flag values.
const (
	AC_SRC_OVER = 0x00
	acSrcOver   = 0x00
)

// newDIBSection creates a top-down 32-bit DIB section together with a memory DC
// holding it. release tears both down.
// The pixel pointer is received into an unsafe.Pointer directly (rather than a
// uintptr that later gets converted), because converting uintptr back to a
// pointer is what go vet's unsafeptr check forbids.
func newDIBSection(refDC uintptr, w, h int) (uintptr, unsafe.Pointer, func(), error) {
	bi := bitmapInfo{
		Size:        uint32(unsafe.Sizeof(bitmapInfo{})),
		Width:       int32(w),
		Height:      -int32(h), // negative => top-down, matching the pixel walk
		Planes:      1,
		BitCount:    32,
		Compression: BI_RGB,
	}
	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIBSection.Call(
		refDC, uintptr(unsafe.Pointer(&bi)), DIB_RGB_COLORS,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 || bits == nil {
		return 0, nil, func() {}, errBitmapCreate
	}

	dc, _, _ := procCreateCompatibleDC.Call(refDC)
	if dc == 0 {
		procDeleteObject.Call(hbm)
		return 0, nil, func() {}, errDCCreate
	}
	old, _, _ := procSelectObject.Call(dc, hbm)

	release := func() {
		procSelectObject.Call(dc, old)
		procDeleteObject.Call(hbm)
		procDeleteDC.Call(dc)
	}
	return dc, bits, release, nil
}

// errBitmapCreate / errDCCreate报告无法为图像分配 GDI 资源。
// 可用 var 以便调用方（包括测试）断言。
var (
	errBitmapCreate = errText("winui: 无法创建 DIB 位图")
	errDCCreate     = errText("winui: 无法创建兼容 DC")
)

// errText 是携带固定描述的错误（避免为两个常量引入 errors 导入）。
type errText string

func (e errText) Error() string { return string(e) }

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

// RenderOverlay returns a copy of base with draw painted on top of it.
//
// It exists so annotations drawn in the editor (which live on the window's DC)
// can be baked into the exported image. The work happens in a DIB section owned
// by Windows: the pixels are written into that buffer, the caller's draw runs
// against its memory DC using the same Canvas API as on-screen painting, and the
// result is read straight back out of the buffer. Doing it this way avoids a
// second drawing implementation (and keeps the file export identical to what the
// user previewed).
func RenderOverlay(base image.Image, draw func(c *Canvas)) *image.RGBA {
	if base == nil {
		return nil
	}
	b := base.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return nil
	}
	defer procReleaseDC.Call(0, screenDC)

	srcDC, bits, release, err := newDIBSection(screenDC, w, h)
	if err != nil {
		return nil
	}
	defer release()

	// Seed the DIB with the base image (32-bit BGRA, top-down).
	pix := unsafe.Slice((*byte)(bits), w*h*4)
	for y := 0; y < h; y++ {
		row := y * w * 4
		for x := 0; x < w; x++ {
			r, g, bl, _ := base.At(b.Min.X+x, b.Min.Y+y).RGBA()
			o := row + x*4
			pix[o+0] = byte(bl >> 8)
			pix[o+1] = byte(g >> 8)
			pix[o+2] = byte(r >> 8)
			pix[o+3] = 0xFF
		}
	}

	if draw != nil {
		draw(&Canvas{hdc: srcDC})
	}

	// Read the composited pixels back out of the same buffer.
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		srcRow := y * w * 4
		for x := 0; x < w; x++ {
			o := srcRow + x*4
			d := y*out.Stride + x*4
			// BGRA -> RGBA.
			out.Pix[d+0] = pix[o+2]
			out.Pix[d+1] = pix[o+1]
			out.Pix[d+2] = pix[o+0]
			out.Pix[d+3] = 0xFF
		}
	}
	return out
}
