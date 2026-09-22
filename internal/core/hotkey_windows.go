//go:build windows

package core

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// Win32 hotkey + message-loop bindings.
//
// GoBox deliberately avoids cgo (see PRD GFR-11), so global hotkeys are
// implemented with RegisterHotKey on a dedicated OS-threaded message loop
// instead of a low-level keyboard hook library.
var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procPeekMessageW       = user32.NewProc("PeekMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessageW   = user32.NewProc("DispatchMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
)

// WM_HOTKEY is posted to the registering thread's queue when a hotkey fires.
const wmHotkey = 0x0312
const wmQuit = 0x0012
const pmRemove = 0x0001

// winHotkeyBackend registers hotkeys on a dedicated message-pump thread.
type winHotkeyBackend struct {
	mu      sync.Mutex
	thread  uint32
	started bool
	stopCh  chan struct{}
	wakeCh  chan struct{}
}

func newHotkeyBackend() hotkeyBackend { return &winHotkeyBackend{} }

// msg mirrors the Win32 MSG structure.
type hotkeyMSG struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
}

func (b *winHotkeyBackend) register(id int, c Combo) error {
	// MOD_NOREPEAT stops auto-repeat from re-firing while held.
	mods := c.Mods | ModNoRepeat
	r, _, err := procRegisterHotKey.Call(0, uintptr(id), uintptr(mods), uintptr(c.VK))
	if r == 0 {
		if err != nil && err != syscall.Errno(0) {
			return err
		}
		return fmt.Errorf("RegisterHotKey 返回 0")
	}
	return nil
}

func (b *winHotkeyBackend) unregister(id int) error {
	r, _, err := procUnregisterHotKey.Call(0, uintptr(id))
	if r == 0 {
		if err != nil && err != syscall.Errno(0) {
			return err
		}
	}
	return nil
}

// run pins to an OS thread and pumps messages, forwarding WM_HOTKEY ids.
func (b *winHotkeyBackend) run(ch chan<- int) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	tid, _, _ := procGetCurrentThreadId.Call()
	b.mu.Lock()
	b.thread = uint32(tid)
	b.started = true
	wake := b.wakeCh
	b.mu.Unlock()
	if wake != nil {
		close(wake)
	}

	var msg hotkeyMSG
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		// 0 = WM_QUIT, ^uintptr(0) = error; both end the loop.
		if int32(r) <= 0 {
			return
		}
		if msg.message == wmHotkey {
			select {
			case ch <- int(msg.wParam):
			default: // press dropped rather than blocking the pump
			}
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// close posts WM_QUIT to the pump thread and waits briefly for it to exit.
func (b *winHotkeyBackend) close() {
	b.mu.Lock()
	tid := b.thread
	wake := b.wakeCh
	b.mu.Unlock()

	if tid != 0 {
		// WM_QUIT(0x0012) wParam=0 lParam=0 terminates GetMessage.
		procPostThreadMessageW.Call(uintptr(tid), wmQuit, 0, 0)
		return
	}
	// Thread not up yet: release anyone waiting for thread readiness.
	if wake != nil {
		b.mu.Lock()
		if b.wakeCh != nil {
			close(b.wakeCh)
			b.wakeCh = nil
		}
		b.mu.Unlock()
	}
}
