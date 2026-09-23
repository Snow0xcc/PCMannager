//go:build windows

package wailsapp

import (
	"context"
	"sync"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/server"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// emitEvent pushes one bus event to the native window via the Wails event API.
func emitEvent(ctx context.Context, ev core.Event) {
	runtime.EventsEmit(ctx, EventBusName, ev)
}

// windowState is the package-level handle to the running native window.
type windowState struct {
	// mu guards every field below.
	mu sync.RWMutex

	// ctx is the Wails runtime context from OnStartup; nil before startup and
	// after shutdown. Only non-nil while the window can accept runtime calls.
	ctx context.Context

	// ready is closed the first time ctx becomes non-nil, and then stays
	// closed: it is a one-shot "the window was up" latch, which is what
	// Available waiters need. Callers that must know whether the window is up
	// *right now* use IsAvailable.
	ready chan struct{}

	// readyOnce makes closing ready idempotent.
	readyOnce sync.Once
}

// win is shared between the goroutine running wails.Run (which writes it from
// OnStartup/OnShutdown) and any caller of Show/Hide/IsAvailable/Available
// (which read it from the tray or elsewhere).
//
// Lock order: win.mu is the only lock in this package. It is a leaf lock — it
// is never held while calling into another package (wails, runtime, server),
// and no other lock is acquired while holding it — so no cycle is possible.
// ready is closed under the same mu as the write that makes ctx non-nil, so a
// reader that observes a non-nil ctx is guaranteed to observe a closed ready.
var win = windowState{ready: make(chan struct{})}

// available is the platform implementation behind the exported Available.
func available() <-chan struct{} {
	win.mu.RLock()
	defer win.mu.RUnlock()
	return win.ready
}

// setWindowCtx publishes (or clears) the runtime context, closing ready the
// first time a context arrives.
func setWindowCtx(ctx context.Context) {
	win.mu.Lock()
	if ctx != nil {
		win.ctx = ctx
		win.readyOnce.Do(func() {
			if win.ready != nil {
				close(win.ready)
			}
		})
	} else {
		win.ctx = nil
	}
	win.mu.Unlock()
}

// windowCtx returns the live runtime context, or nil when no window is up.
func windowCtx() context.Context {
	win.mu.RLock()
	defer win.mu.RUnlock()
	return win.ctx
}

// Show reveals the native window.
//
// The window is created with StartHidden, so nothing is visible until this is
// called; the tray is the primary entry point and calls it on demand.
//
// It returns ErrUnsupported while the window is not running (before OnStartup
// or after shutdown) instead of blocking or panicking: runtime.WindowShow
// fatals on an empty context, so the context is checked first.
func Show() error {
	ctx := windowCtx()
	if ctx == nil {
		return ErrUnsupported
	}
	runtime.WindowShow(ctx)
	return nil
}

// Hide hides the native window without quitting: HideWindowOnClose keeps the
// process alive in the tray, and this is its programmatic counterpart.
//
// It returns ErrUnsupported when no window is running.
func Hide() error {
	ctx := windowCtx()
	if ctx == nil {
		return ErrUnsupported
	}
	runtime.WindowHide(ctx)
	return nil
}

// IsAvailable reports whether the native window is up and can be shown or
// hidden. It is safe to call at any time and never blocks.
func IsAvailable() bool { return windowCtx() != nil }

// run builds and launches the native Wails window, blocking until closed.
//
// Notes on this wiring:
//
//   - Wails is used with CGO_ENABLED=0 throughout (verified for
//     windows/amd64, linux/amd64 and darwin/arm64), so the project's
//     cross-compilation guarantee is preserved.
//   - StartHidden is set because the tray is the primary entry point; the
//     window is shown on demand through Show, which acts on the runtime
//     context published by OnStartup.
//   - HideWindowOnClose keeps the assistant alive in the tray when the user
//     closes the window, matching the tray's expected behaviour.
func run(opts Options, prov server.Provider) error {
	api := NewAPI(prov)

	title := opts.Title
	if title == "" {
		title = "PCMannager"
	}
	width, height := opts.Width, opts.Height
	if width <= 0 {
		width = 1100
	}
	if height <= 0 {
		height = 760
	}

	// Forward bus events for the window's lifetime. A slow frontend must not
	// stall the module that published the event, mirroring the SSE handler's
	// drop-on-full policy.
	//
	// started gates forwarding until a runtime context exists; before that the
	// window cannot receive events, so they are simply skipped.
	ch, unsubscribe := prov.Subscribe()
	defer unsubscribe()

	var (
		cancel  context.CancelFunc
		started = make(chan struct{})
	)
	go func() {
		<-started // wait for OnStartup to publish the runtime context
		ctx := api.ctxFor()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				emitEvent(ctx, ev)
			}
		}
	}()
	var closeOnce sync.Once
	closeStarted := func() { closeOnce.Do(func() { close(started) }) }
	defer closeStarted()
	defer setWindowCtx(nil) // no runtime calls after wails.Run returns

	err := wails.Run(&options.App{
		Title:             title,
		Width:             width,
		Height:            height,
		MinWidth:          720,
		MinHeight:         520,
		StartHidden:       true,
		HideWindowOnClose: true,
		BackgroundColour:  options.NewRGB(18, 20, 26),
		AssetServer: &assetserver.Options{
			Assets: opts.Assets,
		},
		OnStartup: func(ctx context.Context) {
			api.setCtx(ctx)
			// Cancelled in OnShutdown to stop the forwarding goroutine.
			var base context.Context
			base, cancel = context.WithCancel(ctx)
			api.setCtx(base)
			// Publish the package-level context last so Show/Hide only ever
			// see a context that is fully wired up.
			setWindowCtx(base)
			closeStarted()
			if opts.Logf != nil {
				opts.Logf("wailsapp: 原生窗口已启动")
			}
		},
		OnShutdown: func(ctx context.Context) {
			// The frontend is being torn down from here on; dropping the
			// shared context makes Show/Hide report ErrUnsupported instead of
			// posting to a window that no longer exists. The readiness latch
			// stays closed — it is a one-shot "the window came up" signal.
			setWindowCtx(nil)
			if cancel != nil {
				cancel()
			}
			unsubscribe()
			if opts.Logf != nil {
				opts.Logf("wailsapp: 原生窗口已关闭")
			}
		},
		Bind: []interface{}{api},
	})
	if cancel != nil {
		cancel()
	}
	return err
}
