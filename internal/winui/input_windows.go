//go:build windows

package winui

import "unsafe"

// Synthetic input, used by the scrolling-screenshot feature to drive
// auto-scroll. It is deliberately limited to the mouse wheel: that is all the
// capture flow needs, and every extra injected event is another way to disturb
// the user's session.

// mouseInput mirrors the Win32 MOUSEINPUT structure.
type mouseInput struct {
	Dx        int32
	Dy        int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// input mirrors the Win32 INPUT structure for mouse events.
//
// The union in the C definition is represented by the largest member; the type
// field must match, and the padding keeps the struct the size Windows expects
// (INPUT is 40 bytes on x64 with MOUSEINPUT).
type input struct {
	Type uint32
	_    uint32
	Mi   mouseInput
	_    [8]byte
}

// Mouse wheel input flags.
const (
	MOUSEEVENTF_WHEEL = 0x0800
	WHEEL_DELTA       = 120
)

// ScrollWheel injects a vertical wheel scroll at the current cursor position.
//
// notches is in wheel detents (WHEEL_DELTA): positive scrolls up (content moves
// down), negative scrolls down, matching the user-facing convention.
//
// It returns false when SendInput rejected the event, which happens when the
// desktop is locked or the calling process is not on the interactive session.
func ScrollWheel(notches int) bool {
	if notches == 0 {
		return true
	}
	in := input{
		Type: 0, // INPUT_MOUSE
		Mi: mouseInput{
			MouseData: uint32(int32(notches * WHEEL_DELTA)),
			Flags:     MOUSEEVENTF_WHEEL,
		},
	}
	r, _, _ := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
	return r != 0
}

// SetCursorPos moves the OS cursor to the given screen coordinates.
//
// The scrolling-screenshot flow needs this before it injects wheel events: a
// wheel message is delivered to whatever window is under the cursor, so moving
// the pointer into the target window first is what keeps the auto-scroll from
// scrolling some unrelated window (or the system volume popup).
func SetCursorPos(x, y int32) bool {
	r, _, _ := procSetCursorPos.Call(uintptr(x), uintptr(y))
	return r != 0
}

// SendInput is bound lazily like every other Win32 entry point in this package.
var procSendInput = user32.NewProc("SendInput")

// SetCursorPos lives in user32 alongside the other cursor entry points.
var procSetCursorPos = user32.NewProc("SetCursorPos")
