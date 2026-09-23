// Package wailsapp hosts the preferences panel in a native window using Wails
// (WebView2 on Windows), as an alternative to opening the panel in a browser.
//
// It deliberately *coexists* with internal/server rather than replacing it:
//
//   - internal/server stays the cross-platform control surface (HTTP + SSE),
//     so Linux/macOS and headless environments keep working, and its tests
//     remain valid.
//   - This package adds a native Windows window on top of the SAME data
//     contract (server.Provider), which internal/app already implements.
//
// The consequence is that there is one source of truth for panel data: both
// the HTTP handlers and the Wails bindings read from server.Provider.
//
// Dependency direction stays app -> server and app -> wailsapp; nothing here
// reaches back into internal/app.
package wailsapp

import (
	"context"
	"errors"
	"io/fs"
	"sync"

	"github.com/snow0xcc/pcmannager/internal/server"
)

// ErrUnsupported is returned where a native Wails window is not available
// (non-Windows builds, or before/after the window's lifetime on Windows).
// Callers fall back to the HTTP panel.
var ErrUnsupported = errors.New("wailsapp: 当前平台不提供原生窗口，请使用首选项面板")

// EventBusName is the Wails event name the panel subscribes to for live module
// events. It mirrors the SSE stream exposed by internal/server.
const EventBusName = "pcm:event"

// Available returns a channel that is closed when the native window's runtime
// context becomes ready, letting callers wait (or poll) for the window without
// importing the runtime themselves.
//
// Off Windows — where no native window ever exists — the channel is never
// closed, so a bounded wait stays bounded:
//
//	select {
//	case <-wailsapp.Available():
//	    _ = wailsapp.Show()
//	default: // not ready (or unsupported); fall back to the browser
//	}
//
// Implementations live in app_windows.go (closed by OnStartup) and
// app_other.go (never closed).
func Available() <-chan struct{} { return available() }

// API is the object bound to the frontend. Every exported method becomes
// callable from JavaScript via the generated Wails bindings.
//
// It is intentionally a thin adapter over server.Provider: no business logic
// lives here, so the web panel and the native window can never diverge.
type API struct {
	prov server.Provider

	// mu guards ctx, which is written by OnStartup (on the window's goroutine)
	// and read by the event-forwarding goroutine started by run.
	//
	// Lock order: mu is a leaf lock. It is never held while calling into
	// another package, and no other lock is ever acquired while holding it, so
	// no lock cycle can form.
	mu  sync.RWMutex
	ctx context.Context
}

// NewAPI wraps a provider for use as a Wails binding.
func NewAPI(prov server.Provider) *API { return &API{prov: prov} }

// setCtx records the Wails runtime context so events can be emitted.
func (a *API) setCtx(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ctx = ctx
}

// ctxFor returns the runtime context, or a background one before startup.
func (a *API) ctxFor() context.Context {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// State returns the full panel snapshot: version, app settings, modules and
// platform capabilities. Mirrors GET /api/state.
func (a *API) State() map[string]any {
	return map[string]any{
		"version":      a.prov.Version(),
		"app":          a.prov.AppConfig(),
		"modules":      a.prov.Modules(),
		"capabilities": a.prov.Capabilities(),
	}
}

// Modules returns every module snapshot. Mirrors GET /api/modules.
func (a *API) Modules() []server.ModuleInfo { return a.prov.Modules() }

// Module returns one module snapshot. Mirrors GET /api/modules/{id}.
func (a *API) Module(id string) (server.ModuleInfo, bool) { return a.prov.Module(id) }

// PatchModule applies enabled/hotkey/option changes. Mirrors PATCH.
func (a *API) PatchModule(id string, patch server.ModulePatch) error {
	return a.prov.PatchModule(id, patch)
}

// RunAction triggers a declared module action.
func (a *API) RunAction(module, action string, params map[string]string) error {
	return a.prov.RunAction(module, action, params)
}

// RunHotkey invokes a module's hotkey handler manually.
func (a *API) RunHotkey(id string) error { return a.prov.RunHotkey(id) }

// OpenUI opens a module's dedicated window.
func (a *API) OpenUI(id string) error { return a.prov.OpenUI(id) }

// AppConfig returns the application settings.
func (a *API) AppConfig() server.AppConfig { return a.prov.AppConfig() }

// PatchAppConfig applies an incremental application settings change.
func (a *API) PatchAppConfig(patch server.AppConfigPatch) error {
	return a.prov.PatchAppConfig(patch)
}

// Capabilities describes the platform features available in this build.
func (a *API) Capabilities() any { return a.prov.Capabilities() }

// ValidateHotkey checks a hotkey string, returning a reason when invalid.
// It returns "" when the hotkey is acceptable.
func (a *API) ValidateHotkey(hotkey string) string {
	if err := a.prov.ValidateHotkey(hotkey); err != nil {
		return err.Error()
	}
	return ""
}

// Version returns the application version string.
func (a *API) Version() string { return a.prov.Version() }

// Options configures the native window.
type Options struct {
	// Title is the window caption.
	Title string
	// Width/Height are the initial window size in pixels.
	Width  int
	Height int
	// Assets is the embedded frontend (index.html and friends). The caller
	// owns the embed because //go:embed cannot reach outside its own package
	// directory, and the root package is what can see frontend/.
	Assets fs.FS
	// Logf receives lifecycle messages; may be nil.
	Logf func(format string, args ...any)
}

// emitEvent pushes one bus event to the frontend.
//
// It is implemented per platform: Windows uses the Wails event API, other
// platforms no-op because they never run a native window.
//
// Run launches the native window. It blocks until the window is closed.
//
// Implementations live in app_windows.go (real Wails/WebView2) and
// app_other.go (returns ErrUnsupported so callers fall back to HTTP).
//
// Live module events are forwarded from the bus to the frontend for the whole
// lifetime of the window; the subscription is released on shutdown.
//
// The window starts hidden (StartHidden) and closing it only hides it
// (HideWindowOnClose), so Run typically never returns. Because Run blocks, the
// in-process display entry points — Show, Hide, IsAvailable and Available —
// are the way other goroutines (the tray, in particular) reveal the window:
// they act on the runtime context that OnStartup published, and report
// ErrUnsupported while no window is running rather than blocking or panicking.
func Run(opts Options, prov server.Provider) error {
	return run(opts, prov)
}
