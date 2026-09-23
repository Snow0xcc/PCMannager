//go:build !windows

package main

import (
	"github.com/snow0xcc/pcmannager/internal/app"
)

// runNativeWindow is a no-op off Windows: wailsapp.Run reports
// ErrUnsupported there, and internal/server's HTTP panel already covers these
// platforms. Keeping the call site identical avoids a build-tag branch in main.
func runNativeWindow(*app.App, func(string, ...any)) {}
