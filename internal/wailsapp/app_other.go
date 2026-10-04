//go:build !windows

package wailsapp

import (
	"context"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/server"
)

// emitEvent is a no-op: no native window exists to receive events.
func emitEvent(context.Context, core.Event) {}

// Show reports ErrUnsupported: no native window exists to reveal.
//
// It keeps the Windows signature so callers (internal/app) need no build tags,
// and never blocks or panics.
func Show() error { return ErrUnsupported }

// IsAvailable is always false off Windows: no native window is ever running.
func IsAvailable() bool { return false }

// run reports that a native Wails window is unavailable on this platform.
//
// The caller is expected to fall back to the HTTP panel from internal/server,
// which is why this returns ErrUnsupported instead of failing hard.
func run(Options, server.Provider) error { return ErrUnsupported }
