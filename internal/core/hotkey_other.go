//go:build !windows

package core

import "sync"

// noopHotkeyBackend keeps GoBox buildable and runnable on Linux/macOS.
//
// Global hotkeys are a Windows-first feature; on other platforms the panel
// and tray remain the way to invoke modules. Registration reports a clear
// error so the log/panel explains why nothing fires.
type noopHotkeyBackend struct {
	mu      sync.Mutex
	started bool
	closed  bool
	done    chan struct{}
}

func newHotkeyBackend() hotkeyBackend {
	return &noopHotkeyBackend{done: make(chan struct{})}
}

func (b *noopHotkeyBackend) register(int, Combo) error {
	return errHotkeyUnsupported
}

func (b *noopHotkeyBackend) unregister(int) error { return nil }

func (b *noopHotkeyBackend) run(ch chan<- int) {
	b.mu.Lock()
	b.started = true
	closed := b.closed
	b.mu.Unlock()
	if closed {
		// close() already ran before the pump got scheduled (Stop racing
		// Start): exit instead of blocking on a done channel that will never
		// be closed — that would leak this goroutine on every Start/Stop
		// cycle. Mirrors the closed check in the Windows pump loop.
		return
	}
	<-b.done // block until close to mirror the Windows pump
}

func (b *noopHotkeyBackend) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started && !b.closed {
		close(b.done)
	}
	b.closed = true
}
