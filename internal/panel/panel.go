// Package panel owns the single source of truth for the preferences panel UI.
//
// Both transports render this very same file:
//
//   - internal/server serves it over HTTP (browser mode, all platforms).
//   - internal/wailsapp serves it inside a native window (Windows/WebView2).
//
// Keeping one embedded asset (rather than one copy per transport) is what
// prevents the two panels from drifting apart.
//
// //go:embed cannot reference paths outside its own package directory, which is
// why the asset lives here and both consumers import this package.
package panel

import "embed"

//go:embed index.html
var content embed.FS

// FS is the embedded panel asset tree.
func FS() embed.FS { return content }

// IndexPath is the path of the single-page panel inside FS.
const IndexPath = "index.html"
