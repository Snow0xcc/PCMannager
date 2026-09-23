//go:build !ignore_frontend

package server

import (
	"embed"

	"github.com/snow0xcc/pcmannager/internal/panel"
)

// frontend holds the single-page preferences panel.
//
// The panel is embedded so a released binary is self-contained: there is no
// external asset directory to ship or lose. The asset itself is owned by
// internal/panel so the native (Wails) window renders the exact same file.
var frontend embed.FS = panel.FS()
