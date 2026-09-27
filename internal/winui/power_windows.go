//go:build windows

package winui

import "unsafe"

// systemPowerStatus mirrors the Win32 struct filled by GetSystemPowerStatus.
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

// Battery flag bits (SYSTEM_POWER_STATUS.BatteryFlag).
const (
	batteryFlagNoSystemBattery = 128
	batteryFlagUnknown         = 255
)

// PowerStatus reports the system battery state.
//
// present is false on machines without a battery (desktops, VMs) and when the
// values are unavailable, so callers hide the reading instead of showing a
// misleading 0%.
func PowerStatus() (percent int, charging bool, present bool) {
	var st systemPowerStatus
	r, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&st)))
	if r == 0 {
		return 0, false, false
	}
	// 128 = "no system battery", 255 = unknown; either way there is nothing to show.
	if st.BatteryFlag == batteryFlagNoSystemBattery || st.BatteryFlag == batteryFlagUnknown {
		return 0, false, false
	}
	if st.BatteryLifePercent == 255 {
		return 0, false, false
	}
	// ACLineStatus: 1 = plugged in. "Unknown" (255) counts as not charging.
	return int(st.BatteryLifePercent), st.ACLineStatus == 1, true
}

// ContrastText returns black or white, whichever reads better on bg.
//
// The taskbar background follows the system accent by default, which can be
// any colour — including light ones. Hardcoding white text made the readout
// invisible on light themes, so the foreground is derived from the background
// using the standard sRGB relative-luminance weighting.
func ContrastText(bg uint32) uint32 {
	if IsLightColor(bg) {
		return RGB(0, 0, 0) // dark text on a light background
	}
	return RGB(255, 255, 255) // light text on a dark background
}

// IsLightColor reports whether a COLORREF is bright enough to need dark text.
//
// It uses the Rec. 709 luma of the sRGB components — the same weighting the
// eye does — rather than a naive channel average, which would call saturated
// colours (pure blue, for instance) far brighter than they look.
func IsLightColor(bg uint32) bool {
	// COLORREF is 0x00BBGGRR: the low byte is RED, the high byte is BLUE.
	red := float64(bg & 0xFF)
	green := float64((bg >> 8) & 0xFF)
	blue := float64((bg >> 16) & 0xFF)

	luma := 0.2126*red + 0.7152*green + 0.0722*blue
	return luma > 150
}
