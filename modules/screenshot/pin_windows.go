//go:build windows

package screenshot

import (
	"fmt"
	"image"
	"runtime"
	"sync"

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

	// released makes the slot accounting one-shot: the window's own teardown
	// and the error path can both reach it.
	released sync.Once
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

// paint renders the pinned image across the whole client area.
func (p *pinnedWindow) paint(hwnd winui.HWND) {
	c, ps := winui.BeginPaint(hwnd)
	if c.DC() == 0 {
		return
	}
	defer winui.EndPaint(hwnd, ps)

	// The window is created at the image's size, so this is a 1:1 blit (Canvas.Image
	// scales only if the two differ). Going through Canvas.Image rather than
	// StretchDIBits is what keeps a large capture from being painted black: it
	// hands the pixels to Windows in a DIB section.
	c.Image(winui.ClientRect(hwnd), p.img)
}
