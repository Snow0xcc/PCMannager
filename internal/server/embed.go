//go:build !ignore_frontend

package server

import "embed"

// frontend holds the single-page preferences panel.
//
// The panel is embedded so a released binary is self-contained: there is no
// external asset directory to ship or lose.
//
//go:embed web/index.html
var frontend embed.FS
