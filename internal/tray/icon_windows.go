//go:build windows

package tray

import (
	"syscall"
	"unsafe"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// iconFromFile loads a user-configured .ico from disk via LoadImageW
// (LR_LOADFROMFILE|LR_DEFAULTSIZE). LoadImageW is exported from user32.dll —
// the same declaration the shell fallback already uses (procs_windows.go) —
// so this adds no new DLL binding. hInst is ignored with LR_LOADFROMFILE.
func iconFromFile(path string) uintptr {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0 // embedded NUL: reject rather than truncate the path
	}
	const (
		imageIcon      = 1
		lrLoadFromFile = 0x0010
		lrDefaultSize  = 0x0040
	)
	h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(p)), imageIcon, 0, 0,
		lrLoadFromFile|lrDefaultSize)
	return h
}

// iconFromEmbedded rasterises the committed brand .ico at the tray slot size.
// The asset ships 16/32/48/64/256 frames; 32 matches the procedural brand
// mark's size, and the shell scales down to the small-icon metric.
func iconFromEmbedded(string) uintptr {
	img, err := embeddedIconRGBA(32)
	if err != nil {
		return 0
	}
	return winui.IconFromRGBA(img)
}

// iconFromBrand is the procedural butterfly rasterised at runtime — the
// fallback when the embedded asset is missing or corrupt.
func iconFromBrand(string) uintptr { return brandIconHandle() }

// iconFromShell is the chain tail: the icon the exe itself carries (a plain
// `go build` produces none) or, failing that, the shell's generic
// application icon. Never fails in practice, but may still return 0.
func iconFromShell(string) uintptr {
	const (
		imageIcon      = 1
		lrDefaultColor = 0x0000
		lrShared       = 0x8000
		idiApplication = 32512
	)
	h, _, _ := procLoadImageW.Call(0, 0, imageIcon, 0, 0, lrDefaultColor|lrShared)
	if h != 0 {
		return h
	}
	icon, _, _ := procLoadIconW.Call(0, idiApplication)
	return icon
}

// iconHandle resolves the tray icon once per winTray and caches it.
//
// Cached because LR_LOADFROMFILE hands out a fresh HICON on every call, so a
// Hide/Show cycle would otherwise leak one per toggle; the path itself cannot
// change without a restart (restartRequiredKeys), so there is nothing to
// invalidate. A configured path that fails to load is logged once — the
// fallback still gives the user an icon, but the misconfiguration would
// otherwise be invisible (GUI builds have no console to notice on).
//
// The cached handle is deliberately NOT DestroyIcon'd in Destroy(): it is a
// single handle held for the process lifetime (the tray is a singleton), and
// Destroy() runs while the shell may still be drawing it. Freeing it would
// add a teardown ordering hazard for no measurable gain. Same policy as the
// package-level brandIcon, which is shared across winTray instances and
// therefore cannot be owned by any one of them.
func (t *winTray) iconHandle() uintptr {
	t.iconOnce.Do(func() {
		h, src := resolveIcon(t.iconPath,
			iconFromFile, iconFromEmbedded, iconFromBrand, iconFromShell)
		if t.iconPath != "" && src != iconSourceConfig {
			t.log.Warn("自定义托盘图标加载失败，已回退",
				"path", t.iconPath, "source", string(src))
		}
		t.iconH = h
	})
	return t.iconH
}
