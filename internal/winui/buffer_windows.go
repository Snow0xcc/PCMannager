//go:build windows

package winui

import (
	"runtime"
	"sync/atomic"
	"unsafe"
)

// 双缓冲绘制支持。
//
// 一次绘制过程往往包含多次 blit/遮罩/图形调用：如果直接画到窗口 DC 上，屏幕会
// 依次闪过每个中间状态（截图编辑器重绘整屏时尤其明显）。双缓冲把整个绘制过程
// 先合成为内存位图，再一次 BitBlt 上屏，中间状态用户完全看不到。

// 手动双缓冲所需的 GDI proc 就近声明，与 draw_windows.go 里的绘图原语同源。
// 挂错 DLL 会在首次调用时 panic（不是启动时），故这里同样只从 gdi32/user32
// 取各自名下的导出：CreateCompatibleDC/CreateDIBSection/BitBlt/SelectObject/
// DeleteDC 属 gdi32，GetDC/ReleaseDC 属 user32。
var (
	procGetDCBuf              = user32.NewProc("GetDC")
	procReleaseDCBuf          = user32.NewProc("ReleaseDC")
	procCreateCompatibleDCBuf = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDCBuf           = gdi32.NewProc("DeleteDC")
	procSelectObjectBuf       = gdi32.NewProc("SelectObject")
	procCreateDIBSectionBuf   = gdi32.NewProc("CreateDIBSection")
	procBitBltBuf             = gdi32.NewProc("BitBlt")
)

// BeginBufferedPaint wraps a window DC in a back buffer, so a paint pass that
// draws many times (image blits, alpha masks, shapes) composites once instead
// of flickering through the intermediate states on screen.
// The returned done func must be called (defer) to blit the buffer to screen;
// it also releases all GDI objects created for the buffer.
//
// 实现走手动内存 DC 双缓冲（CreateCompatibleDC + CreateDIBSection + BitBlt），
// 而不是 UxTheme 的 BeginBufferedPaint：
//   - 所需 GDI 原语与 draw_windows.go 的 Canvas.Image 完全同源，行为已被现有
//     测试覆盖——特别是"Go 堆上的大源缓冲会被 StretchDIBits 静默失败"的坑
//     （512x512 成功、768x768 起返回 0），这里用 CreateDIBSection 让 Windows
//     持有像素，天然避开；
//   - UxTheme 路径要额外操心 BPBF 布局选择与 BPF_COMPOSITED/DWM 行为，其
//     BPBF_COMPATIBLEBITMAP 在 DWM 桌面上还有"位图布局不保证"的文档警告；
//     手动路径的失败模式简单（句柄非零即成功），排障面小。
//
// 时序要求：done 必须 defer 到绘制完成后再调——先画完（多次 Fill/Image/
// FillAlpha 都落在内存 DC 上），done 里一次性 BitBlt(SRCCOPY) 上屏并释放全部
// GDI 对象。done 幂等：重复调用是 no-op，不会二次上屏或重复释放。
func BeginBufferedPaint(hwnd HWND) (*Canvas, func(), error) {
	// 用屏幕 DC 作参考上下文：CreateCompatibleDC/CreateDIBSection 需要一个 DC
	// 来决定兼容位图的像素格式（色深须与屏幕一致，否则上屏 blit 时发生颜色
	// 转换，文字与遮罩会发虚）。
	screenDC, _, _ := procGetDCBuf.Call(0)
	if screenDC == 0 {
		return nil, noopDone, errBufferedPaint
	}
	fail := func(err error) (*Canvas, func(), error) {
		procReleaseDCBuf.Call(0, screenDC)
		return nil, noopDone, err
	}

	// 尺寸取窗口当前客户区（设备像素），与 WM_PAINT 的绘制范围一致。
	var rc Rect
	if r, _, _ := procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rc))); r == 0 {
		return fail(errBufferedPaint)
	}
	w, h := int(rc.Width()), int(rc.Height())
	if w <= 0 || h <= 0 {
		return fail(errBufferedPaint)
	}

	memDC, _, _ := procCreateCompatibleDCBuf.Call(screenDC)
	if memDC == 0 {
		return fail(errBufferedPaint)
	}

	// 用 CreateDIBSection 而非 CreateCompatibleBitmap 做缓冲：像素内存由
	// Windows 持有（同一理由见 Canvas.Image 的注释），后续如需读写像素也不
	// 经过 Go 堆。
	bi := bitmapInfo{
		Size:        uint32(unsafe.Sizeof(bitmapInfo{})),
		Width:       int32(w),
		Height:      -int32(h), // 负高 = top-down，与 Canvas.Image 的像素步进方向一致
		Planes:      1,
		BitCount:    32,
		Compression: BI_RGB,
	}
	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIBSectionBuf.Call(
		screenDC, uintptr(unsafe.Pointer(&bi)), DIB_RGB_COLORS,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 || bits == nil {
		procDeleteDCBuf.Call(memDC)
		return fail(errBufferedPaint)
	}
	old, _, _ := procSelectObjectBuf.Call(memDC, hbm)

	// 每次调用独立的幂等标志：done 可能被 defer 和显式各调一次，只应生效一次；
	// 但两次独立的 BeginBufferedPaint 周期互不影响，故不能放在包级。
	var doneOnce atomic.Uint32
	done := func() {
		if doneOnce.Swap(1) == 1 {
			return // 幂等：二次调用不再上屏，也不重复释放
		}
		// 整块客户区一次性上屏：绘制期间的一切中间状态只存在于内存位图里，
		// 屏幕只看到最终结果。
		procBitBltBuf.Call(uintptr(hwnd), 0, 0, uintptr(w), uintptr(h),
			memDC, 0, 0, SRCCOPY)
		procSelectObjectBuf.Call(memDC, old)
		procDeleteObject.Call(hbm)
		procDeleteDCBuf.Call(memDC)
		procReleaseDCBuf.Call(0, screenDC)
		// 保证 bits 与窗口句柄在上述调用结束前不被 GC 回收。
		runtime.KeepAlive(bits)
		runtime.KeepAlive(hwnd)
	}

	return &Canvas{hdc: memDC}, done, nil
}

// noopDone 是失败路径返回的空操作闭包，调用方无需先判错即可统一 defer。
func noopDone() {}

// errBufferedPaint 报告无法建立双缓冲（任一 GDI 步骤失败即整体失败）。
var errBufferedPaint = errText("winui: 无法创建双缓冲绘制表面")
