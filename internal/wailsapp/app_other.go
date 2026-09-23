//go:build !windows

package wailsapp

import (
	"context"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/server"
)

// emitEvent is a no-op: no native window exists to receive events.
func emitEvent(context.Context, core.Event) {}

// Never-ready stand-in for the Windows-side window latch.
//
// Off Windows no window ever starts, so this channel is never closed: waiting
// on it blocks forever and selecting on it always takes the default branch.
// (It is local rather than a package var so that nothing can close it.)
var neverReady = make(chan struct{})

// available is the platform implementation behind the exported Available. Off
// Windows it returns a channel that is never closed: there is no window to
// become ready, and callers must fall back to the HTTP panel.
func available() <-chan struct{} { return neverReady }

// Show reports ErrUnsupported: no native window exists to reveal.
//
// It keeps the Windows signature so callers (internal/app) need no build tags,
// and never blocks or panics.
func Show() error { return ErrUnsupported }

// Hide reports ErrUnsupported: no native window exists to hide.
func Hide() error { return ErrUnsupported }

// IsAvailable is always false off Windows: no native window is ever running.
func IsAvailable() bool { return false }

// run reports that a native Wails window is unavailable on this platform.
//
// The caller is expected to fall back to the HTTP panel from internal/server,
// which is why this returns ErrUnsupported instead of failing hard.
func run(Options, server.Provider) error { return ErrUnsupported }
