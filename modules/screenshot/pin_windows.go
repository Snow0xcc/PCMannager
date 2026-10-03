//go:build windows

package screenshot

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"math"
	"runtime"
	"sync"
	"time"

	clip "golang.design/x/clipboard"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/winui"
)

// Pinning turns a capture into a small borderless window that stays above every
// other window, so a screenshot can be kept in sight while working (the
// Snipaste "pin to screen" behaviour).
//
// Each pin owns its window, its thread and its lifetime: pinImage returns as
// soon as the window has been handed to its own goroutine, and the user closes
// it with the right mouse button or Esc. Nothing here touches the editor, which
// is why a pin survives the editor closing.

// pinMaxWindows caps how many pins can exist at once.
//
// Pins are cheap individually but a stuck mouse button or a hurried double
// click can spray them across the screen, and each one holds a copy of a
// full-screen bitmap: the cap keeps a slip from exhausting memory, and hitting
// it is reported rather than silently ignored.
const pinMaxWindows = 50

var (
	// pinMu guards pinActive.
	pinMu sync.Mutex
	// pinActive is how many pin windows are currently on screen.
	pinActive int
)

// pinnedCount reports how many pinned windows are currently on screen.
func pinnedCount() int {
	pinMu.Lock()
	defer pinMu.Unlock()
	return pinActive
}

// pinReserve claims a slot for a new pin, or reports why it cannot.
func pinReserve() error {
	pinMu.Lock()
	defer pinMu.Unlock()
	if pinActive >= pinMaxWindows {
		return fmt.Errorf("screenshot: 贴图数量已达上限 %d，请先关闭一些", pinMaxWindows)
	}
	pinActive++
	return nil
}

// pinRelease returns a slot to the pool. It is safe to call more than once per
// pin only through pinnedWindow.release, which funnels it through a sync.Once.
func pinRelease() {
	pinMu.Lock()
	if pinActive > 0 {
		pinActive--
	}
	pinMu.Unlock()
}

// pinnedWindow is the state of one pin, owned by the thread that created its
// window: every field is written there before the message loop starts, so no
// locking is needed for them.
type pinnedWindow struct {
	ctx *core.Context
	img image.Image
	win *winui.Window

	// zoom is the current scale factor, clamped to [pinZoomMin, pinZoomMax].
	// Written only on the window's own thread (WM_MOUSEWHEEL), so no lock.
	zoom float64
	// wheelAcc accumulates raw wheel deltas until a full notch (±120) is
	// reached; high-resolution wheels deliver smooth small deltas.
	wheelAcc int

	// released makes the slot accounting one-shot: the window's own teardown
	// and the error path can both reach it.
	released sync.Once
}

// Pin zoom bounds. 0.1 keeps a shrunken pin findable; 8× exhausts screen space
// long before it exhausts memory.
const (
	pinZoomMin = 0.1
	pinZoomMax = 8.0
)

// clampZoom keeps the scale factor inside the supported range.
func clampZoom(z float64) float64 {
	if z < pinZoomMin {
		return pinZoomMin
	}
	if z > pinZoomMax {
		return pinZoomMax
	}
	return z
}

// zoomAnchor maps a cursor position (in window/client coordinates) to the
// image-space point that sits under it at the given zoom.
//
// Keeping this point fixed under the cursor while the window resizes is what
// makes the zoom feel anchored to the pointer rather than to the top-left
// corner: the pin's new top-left must move by ax*newZoom - cursorX.
func zoomAnchor(cursorX, zoom float64) float64 {
	if zoom <= 0 {
		zoom = 1
	}
	return cursorX / zoom
}

// zoomedOrigin computes the pin's new top-left so that image-space point ax
// stays under the cursor after scaling to newZoom.
func zoomedOrigin(cursorScreenX int32, ax, newZoom float64) int32 {
	return cursorScreenX - int32(ax*newZoom)
}

// wheelStep accumulates raw wheel deltas and reports whole notches.
//
// High-resolution wheels deliver deltas in small increments whose sum reaches
// ±120 per physical notch; acting on every raw delta would zoom in jittery
// sub-steps, so they are accumulated here.
func wheelStep(acc, delta int) (newAcc, steps int) {
	acc += delta
	for acc >= 120 {
		steps++
		acc -= 120
	}
	for acc <= -120 {
		steps--
		acc += 120
	}
	return acc, steps
}

// release gives the pin's slot back exactly once.
func (p *pinnedWindow) release() { p.released.Do(pinRelease) }

// pinImage shows img as a borderless always-on-top window at screen position pos.
// It returns immediately; the window owns its own thread and lifetime.
func pinImage(ctx *core.Context, img image.Image, pos image.Point) error {
	// A nil ctx means the module is not wired up; reporting beats dereferencing
	// it in the goroutine below, where the panic would be unrecoverable for the
	// caller.
	if ctx == nil {
		return fmt.Errorf("screenshot: 贴图缺少模块上下文")
	}
	if img == nil {
		return fmt.Errorf("screenshot: 没有可贴图的图像")
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return fmt.Errorf("screenshot: 贴图图像尺寸无效")
	}
	if err := pinReserve(); err != nil {
		ctx.Logger.Warn("贴图被拒绝：数量已达上限", "module", moduleID, "max", pinMaxWindows)
		return err
	}

	go func() {
		// A window belongs to the thread that created it, and its messages are
		// only deliverable there (and DestroyWindow only works there). This
		// goroutine therefore keeps its OS thread for the window's whole life;
		// it must not unlock, because ending the goroutine while locked would
		// retire the thread and its message queue.
		runtime.LockOSThread()
		if err := pinRun(ctx, img, pos, w, h); err != nil {
			pinRelease()
			ctx.Logger.Error("贴图失败", "module", moduleID, "err", err)
		}
	}()
	return nil
}

// pinRun creates the pin window and pumps its messages until it is closed.
func pinRun(ctx *core.Context, img image.Image, pos image.Point, w, h int) error {
	winui.SetDPIAware()

	p := &pinnedWindow{ctx: ctx, img: img}
	// WS_EX_TOPMOST is the whole point of a pin (it must float above the work
	// being done), and WS_EX_TOOLWINDOW keeps it out of the taskbar and Alt+Tab
	// so a pinned screenshot does not masquerade as a document.
	win, err := winui.NewWindow("GoBoxPin", winui.WS_POPUP,
		winui.WS_EX_TOPMOST|winui.WS_EX_TOOLWINDOW, winui.Invalid)
	if err != nil {
		return err
	}
	p.win = win
	win.Handle = p.proc

	if err := winui.SetWindowPos(win.HWND(), winui.Invalid,
		int32(pos.X), int32(pos.Y), int32(w), int32(h),
		winui.SWP_NOZORDER|winui.SWP_SHOWWINDOW); err != nil {
		ctx.Logger.Warn("定位贴图窗口失败", "module", moduleID, "err", err)
	}
	win.Show()
	// A WS_POPUP window is not activated by showing it; the Z-order bump is what
	// puts the pin above the window the user is working in.
	winui.BringToTop(win.HWND())

	// Closing the pin when the application shuts down keeps the locked thread
	// from outliving the process it belongs to. PostMessage is safe from another
	// thread, so this watcher needs no access to the window itself.
	if ctx.Ctx != nil {
		go func() {
			<-ctx.Ctx.Done()
			if winui.IsWindow(win.HWND()) {
				winui.PostMessage(win.HWND(), winui.WM_CLOSE, 0, 0)
			}
		}()
	}

	ctx.Logger.Info("已贴图到屏幕", "module", moduleID,
		"x", pos.X, "y", pos.Y, "w", w, "h", h)

	winui.MessageLoop(nil)

	// The loop ends at WM_DESTROY, which already released the slot; this covers
	// the paths that never got that far and is a no-op otherwise.
	p.release()
	return nil
}

// proc dispatches a pin window's messages.
func (p *pinnedWindow) proc(hwnd winui.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case winui.WM_PAINT:
		p.paint(hwnd)
		return 0, true
	case winui.WM_ERASEBKGND:
		// The painter covers the client area with the image; letting Windows
		// erase first would only add a flicker.
		return 1, true
	case edWmLButtonDown:
		// A WS_POPUP window has no caption to grab, so the drag is delegated to
		// DefWindowProc: this is the standard way to move a borderless window
		// without reimplementing hit-testing and the drag loop.
		winui.BeginDragWindow(hwnd)
		return 0, true
	case winui.WM_LBUTTONDBLCLK:
		// 双击 = 复制到剪贴板 + 关闭（Snipaste 语义）。
		// 顺序很重要：复制是同步操作，窗口销毁是异步 Post——先复制保证
		// 剪贴板内容取自销毁前的窗口数据；复制失败不阻止关闭（用户还能
		// 从历史里再取）。
		p.copyToClipboard()
		winui.PostMessage(hwnd, winui.WM_CLOSE, 0, 0)
		return 0, true
	case winui.WM_MOUSEWHEEL:
		p.onWheel(hwnd, wParam, lParam)
		return 0, true
	case winui.WM_RBUTTONUP:
		// Right click closes, matching the screenshot overlay's own gesture and
		// leaving no decoration on a window whose only purpose is to show pixels.
		winui.PostMessage(hwnd, winui.WM_CLOSE, 0, 0)
		return 0, true
	case edWmKeyDown:
		if int(wParam) == edVkEscape {
			winui.PostMessage(hwnd, winui.WM_CLOSE, 0, 0)
			return 0, true
		}
		return 0, false
	case winui.WM_CLOSE:
		// Destroy must run on the owning thread; this handler is on it.
		if p.win != nil {
			p.win.Destroy()
		}
		return 0, true
	case winui.WM_DESTROY:
		p.release()
		winui.PostQuitMessage(0)
		return 0, true
	}
	return 0, false
}

// copyToClipboard pushes the pinned image onto the clipboard as PNG.
//
// 失败只记日志与面板提示：双击的主要意图仍是关闭，剪贴板写入失败不应把
// 窗口留在屏幕上（用户右键/Esc 还能关，但双击失败后不关反而困惑）。
func (p *pinnedWindow) copyToClipboard() {
	var buf bytes.Buffer
	if err := png.Encode(&buf, p.img); err != nil {
		p.ctx.Logger.Error("贴图 PNG 编码失败", "module", moduleID, "err", err)
		return
	}
	if err := clip.Init(); err != nil {
		p.ctx.Logger.Error("贴图剪贴板不可用", "module", moduleID, "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := clip.Write(ctx, clip.FmtImage, buf.Bytes()); err != nil {
		p.ctx.Logger.Warn("贴图复制到剪贴板失败", "module", moduleID, "err", err)
		p.ctx.Bus.Notice(moduleID, "贴图复制失败，已仅关闭")
		return
	}
	p.ctx.Logger.Info("贴图已复制到剪贴板并关闭", "module", moduleID, "bytes", buf.Len())
}

// onWheel handles zooming around the cursor position.
//
// lParam 是屏幕坐标（WS_POPUP 无边框，客户区原点 == 窗口原点，直接相减即得
// 客户区坐标）；wParam 高 16 位是滚轮增量（正 = 向上/放大）。
func (p *pinnedWindow) onWheel(hwnd winui.HWND, wParam, lParam uintptr) {
	delta := int(int16(wParam >> 16))
	var steps int
	p.wheelAcc, steps = wheelStep(p.wheelAcc, delta)
	if steps == 0 {
		return
	}

	cur := winui.CursorPos()
	wr := winui.WindowRect(hwnd)
	// 客户区坐标（无边框窗口：screen - window 原点）。
	cx := float64(cur.X - wr.Left)
	cy := float64(cur.Y - wr.Top)

	// 指针下的图像点（缩放前后都必须留在指针下）。
	ax, ay := zoomAnchor(cx, p.zoom), zoomAnchor(cy, p.zoom)

	p.zoom = clampZoom(p.zoom * math.Pow(1.2, float64(steps)))

	newW := int32(float64(p.img.Bounds().Dx()) * p.zoom)
	newH := int32(float64(p.img.Bounds().Dy()) * p.zoom)
	if newW < 32 {
		newW = 32
	}
	if newH < 32 {
		newH = 32
	}
	// 让锚点保持在指针下：新原点 = 指针屏幕坐标 - 锚点在新尺寸下的偏移。
	newLeft := zoomedOrigin(cur.X, ax, p.zoom)
	newTop := zoomedOrigin(cur.Y, ay, p.zoom)
	_ = winui.MoveWindow(hwnd, newLeft, newTop, newW, newH, true)
}

// paint renders the pinned image across the whole client area.
func (p *pinnedWindow) paint(hwnd winui.HWND) {
	c, ps := winui.BeginPaint(hwnd)
	if c.DC() == 0 {
		return
	}
	defer winui.EndPaint(hwnd, ps)

	// 缩放后窗口尺寸 ≠ 图像尺寸，Canvas.Image 自动走 HALFTONE StretchBlt
	// 分支做平滑缩放；1:1 时仍是快速 BitBlt。经 Canvas.Image 而非
	// StretchDIBits 是为了避免 Go 堆大缓冲静默失败（见 draw_windows.go）。
	c.Image(winui.ClientRect(hwnd), p.img)
}
