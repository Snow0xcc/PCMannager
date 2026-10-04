//go:build windows

package winui

import (
	"image"
	"unsafe"
)

// 图标创建只用到这几个 GDI/user32 入口，故就近声明。CreateDIBSection、
// CreateCompatibleDC/DeleteDC、DeleteObject、GetDC/ReleaseDC、SendMessageW 与
// RtlMoveMemory 本包已声明（api_windows.go / draw_windows.go），这里直接复用，
// 重复声明会编译失败。挂错 DLL 不会在启动时报错，而是首次调用 panic —— 注意
// CreateIconIndirect/DestroyIcon 在 user32，CreateBitmap 在 gdi32。
var (
	procCreateBitmap       = gdi32.NewProc("CreateBitmap")
	procCreateIconIndirect = user32.NewProc("CreateIconIndirect")
	procDestroyIcon        = user32.NewProc("DestroyIcon")
)

// ICONINFO mirrors the Win32 ICONINFO structure. Field order and widths must
// match exactly: CreateIconIndirect reads hbmMask/hbmColor at the offsets the C
// layout dictates, and getting them wrong yields a garbage handle rather than an
// error.
type ICONINFO struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr
	HbmColor uintptr
}

// IconFromRGBA builds a Windows icon (HICON) from a 32-bit straight-alpha image.
// The caller owns the handle and must release it with DestroyIcon.
// Returns 0 when the icon could not be created.
func IconFromRGBA(img *image.RGBA) uintptr {
	if img == nil {
		return 0
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return 0
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return 0
	}
	defer procReleaseDC.Call(0, screenDC)

	// 颜色位图直接用 32bpp 顶朝下 DIB section：CreateIconIndirect 会拷走位图，
	// 且我们只往 bits 里写像素，不需要内存 DC 参与绘制。
	//
	// 不用 StretchDIBits 灌像素：该 API 在源缓冲区大且位于 Go 堆时会静默失败
	// （见 draw_windows.go 中 Canvas.Image 的注释）。这里改成把 Go 侧缓冲按行
	// 经 RtlMoveMemory 写进 DIB 自己的内存。
	bi := bitmapInfo{
		Size:        uint32(unsafe.Sizeof(bitmapInfo{})),
		Width:       int32(w),
		Height:      -int32(h), // 负高度 => 顶朝下，与 img 的行序一致
		Planes:      1,
		BitCount:    32,
		Compression: BI_RGB,
	}
	var bits unsafe.Pointer
	color, _, _ := procCreateDIBSection.Call(
		screenDC, uintptr(unsafe.Pointer(&bi)), DIB_RGB_COLORS,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 || bits == nil {
		return 0
	}
	defer procDeleteObject.Call(color)

	// 逐行把 RGBA 翻成 DIB 需要的 BGRA 写进去。
	// 用 img.PixOffset 求行首偏移：第 y 行起点是 y*Stride，而 Stride 可能大于
	// 4*width（裁剪出的子图尤其如此）；整块 copy 会把行尾填充一起搬过去，
	// 画面斜移。目标地址只做 uintptr 运算后交给系统调用，从不转回 Go 指针，
	// 因此不触犯 go vet 的 unsafeptr 检查（与 copyToAddress 同法）。
	base := uintptr(bits)
	row := make([]byte, w*4)
	for y := 0; y < h; y++ {
		srcOff := img.PixOffset(b.Min.X, b.Min.Y+y)
		srcRow := img.Pix[srcOff : srcOff+w*4]
		for x := 0; x < w; x++ {
			row[x*4+0] = srcRow[x*4+2] // B
			row[x*4+1] = srcRow[x*4+1] // G
			row[x*4+2] = srcRow[x*4+0] // R
			row[x*4+3] = srcRow[x*4+3] // A
		}
		procRtlMoveMemory.Call(base+uintptr(y*w*4),
			uintptr(unsafe.Pointer(&row[0])), uintptr(len(row)))
	}
	runtimeKeepAlive(row)

	// mask 位图：32bpp 带 alpha 的图标靠 alpha 通道决定透明度，AND mask 必须
	// 全 0 才不会被当作"挖空"区域。CreateBitmap 的 lpBits 传 NULL 时内容是未
	// 定义的，所以显式给一块清零缓冲，而不是赌系统会给零。
	// 1bpp 每行按 32 位（4 字节）对齐。
	maskStride := ((w + 31) / 32) * 4
	maskBits := make([]byte, maskStride*h)
	mask, _, _ := procCreateBitmap.Call(uintptr(w), uintptr(h), 1, 1,
		uintptr(unsafe.Pointer(&maskBits[0])))
	runtimeKeepAlive(maskBits)
	if mask == 0 {
		return 0
	}
	defer procDeleteObject.Call(mask)

	// FIcon = TRUE 表示这是图标（而非光标），故 hotspot 无意义、留 0。
	info := ICONINFO{FIcon: 1, HbmColor: color, HbmMask: mask}
	icon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	runtimeKeepAlive(info)
	if icon == 0 {
		return 0
	}
	// CreateIconIndirect 已复制位图，两个 GDI 位图交由上面的 defer 释放即可，
	// 返回的 icon 归调用方所有。
	return icon
}

// DestroyIconHandle releases an icon created by IconFromRGBA.
func DestroyIconHandle(h uintptr) {
	if h == 0 {
		return
	}
	procDestroyIcon.Call(h)
}
