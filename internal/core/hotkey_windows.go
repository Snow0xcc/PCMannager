//go:build windows

package core

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Win32 hotkey + message-loop bindings.
//
// GoBox deliberately avoids cgo (see PRD GFR-11), so global hotkeys are
// implemented with RegisterHotKey on a dedicated OS-threaded message loop
// instead of a low-level keyboard hook library.
//
// Threading contract (P0-2): RegisterHotKey(NULL, ...) binds the hotkey to the
// *calling* thread's message queue, and WM_HOTKEY is posted to that same queue.
// Calling register() from an HTTP handler or any temporary goroutine therefore
// delivers presses to a queue nobody pumps, so hotkeys silently never fire.
// Every registration is now executed by the pump thread itself: callers post a
// command and wait for its result.
var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessageW   = user32.NewProc("DispatchMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
)

// Windows message ids used by the pump.
const (
	wmHotkey = 0x0312
	wmQuit   = 0x0012
	// wmWake is a private message (WM_APP+1) posted purely to break
	// GetMessage out of its block when a command is queued.
	wmWake = 0x8001
)

// How long a caller waits for the pump to become ready, for a command result,
// and for the pump thread to exit during close. Bounded so a wedged pump can
// never hang shutdown (it is reported instead).
const (
	readyTimeout = 3 * time.Second
	cmdTimeout   = 3 * time.Second
	joinTimeout  = 3 * time.Second
)

// Errors surfaced when the pump is not able to service a command.
var (
	errHotkeyPumpStopped = errors.New("core: 热键消息泵已停止")
	errHotkeyPumpTimeout = errors.New("core: 热键消息泵响应超时")
)

// Command ops executed by the pump thread.
const (
	opRegister = iota
	opUnregister
)

// hotkeyCmd is one unit of work for the pump thread. resp is buffered (cap 1)
// so the pump never blocks even if the caller timed out and walked away.
type hotkeyCmd struct {
	op    int
	id    int
	combo Combo
	resp  chan error
}

// winHotkeyBackend registers hotkeys on a dedicated message-pump thread.
type winHotkeyBackend struct {
	mu     sync.Mutex
	thread uint32
	closed bool

	cmdCh   chan hotkeyCmd
	readyCh chan struct{}
	doneCh  chan struct{}

	// readyOnce guards readyCh so both run() and close() may signal it
	// without a double-close panic when Stop races Start.
	readyOnce sync.Once
}

func newHotkeyBackend() hotkeyBackend {
	return &winHotkeyBackend{
		cmdCh:   make(chan hotkeyCmd, 16),
		readyCh: make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

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

// ready marks the pump as able to accept commands (or releases waiters if it
// will never start).
func (b *winHotkeyBackend) ready() { b.readyOnce.Do(func() { close(b.readyCh) }) }

// waitReady blocks until the pump is running, or the backend is closed.
func (b *winHotkeyBackend) waitReady() error {
	select {
	case <-b.readyCh:
		b.mu.Lock()
		closed := b.closed
		b.mu.Unlock()
		if closed {
			return errHotkeyPumpStopped
		}
		return nil
	case <-time.After(readyTimeout):
		return errHotkeyPumpTimeout
	}
}

// register installs a hotkey by asking the pump thread to do it.
func (b *winHotkeyBackend) register(id int, c Combo) error {
	if err := b.waitReady(); err != nil {
		return err
	}
	return b.exec(hotkeyCmd{op: opRegister, id: id, combo: c, resp: make(chan error, 1)})
}

// unregister removes a hotkey, also on the pump thread.
func (b *winHotkeyBackend) unregister(id int) error {
	if err := b.waitReady(); err != nil {
		// A hotkey that was never registered cannot need removing; failing
		// here would abort Stop, so report success.
		return nil
	}
	return b.exec(hotkeyCmd{op: opUnregister, id: id, resp: make(chan error, 1)})
}

// exec posts a command to the pump and waits for its result.
func (b *winHotkeyBackend) exec(cmd hotkeyCmd) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errHotkeyPumpStopped
	}
	tid := b.thread
	b.mu.Unlock()

	b.cmdCh <- cmd
	// Wake the pump: it may be blocked inside GetMessage.
	if tid != 0 {
		procPostThreadMessageW.Call(uintptr(tid), wmWake, 0, 0)
	}

	select {
	case err := <-cmd.resp:
		return err
	case <-b.doneCh:
		return errHotkeyPumpStopped
	case <-time.After(cmdTimeout):
		return errHotkeyPumpTimeout
	}
}

// run pins to an OS thread and pumps messages, executing queued commands and
// forwarding WM_HOTKEY ids.
func (b *winHotkeyBackend) run(ch chan<- int) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(b.doneCh)

	tid, _, _ := procGetCurrentThreadId.Call()
	b.mu.Lock()
	b.thread = uint32(tid)
	b.mu.Unlock()
	// Signal readiness only after the thread id is published, so a caller
	// woken by ready() can always post a wake message to the right thread.
	b.ready()

	var msg hotkeyMSG
	for {
		// Service commands before blocking, and again after every wake.
		b.drain()

		b.mu.Lock()
		closed := b.closed
		b.mu.Unlock()
		if closed {
			return
		}

		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		// 0 = WM_QUIT, ^uintptr(0) = error; both end the loop.
		if int32(r) <= 0 {
			return
		}
		switch msg.message {
		case wmWake:
			// Posted only to break the block; commands are drained at the top.
			continue
		case wmHotkey:
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

// drain executes every queued command without blocking.
func (b *winHotkeyBackend) drain() {
	for {
		select {
		case cmd := <-b.cmdCh:
			b.doCmd(cmd)
		default:
			return
		}
	}
}

// doCmd performs one command. It runs exclusively on the pump thread, which is
// exactly why RegisterHotKey now matches the thread that pumps WM_HOTKEY.
func (b *winHotkeyBackend) doCmd(cmd hotkeyCmd) {
	var err error
	switch cmd.op {
	case opRegister:
		err = b.doRegister(cmd.id, cmd.combo)
	case opUnregister:
		err = b.doUnregister(cmd.id)
	}
	cmd.resp <- err
}

// doRegister calls RegisterHotKey. Must run on the pump thread.
func (b *winHotkeyBackend) doRegister(id int, c Combo) error {
	// MOD_NOREPEAT stops auto-repeat from re-firing while held.
	mods := c.Mods | ModNoRepeat
	r, _, err := procRegisterHotKey.Call(0, uintptr(id), uintptr(mods), uintptr(c.VK))
	if r == 0 {
		if err != nil && err != syscall.Errno(0) {
			return fmt.Errorf("RegisterHotKey 失败: %w", err)
		}
		return fmt.Errorf("RegisterHotKey 返回 0（可能被其它程序占用）")
	}
	return nil
}

// doUnregister calls UnregisterHotKey. Must run on the pump thread.
func (b *winHotkeyBackend) doUnregister(id int) error {
	r, _, err := procUnregisterHotKey.Call(0, uintptr(id))
	if r == 0 {
		if err != nil && err != syscall.Errno(0) {
			return fmt.Errorf("UnregisterHotKey 失败: %w", err)
		}
	}
	return nil
}

// close stops the pump and waits for the thread to exit, so no hotkey, thread
// or goroutine outlives Stop. It is safe to call more than once, and safe to
// call before the pump ever started.
func (b *winHotkeyBackend) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		<-b.doneCh // still join, so repeated Stop waits for the same exit
		return
	}
	b.closed = true
	tid := b.thread
	b.mu.Unlock()

	// Release anyone waiting for readiness if the pump never came up.
	b.ready()

	if tid != 0 {
		// WM_QUIT terminates GetMessage; the loop then sees closed and returns.
		procPostThreadMessageW.Call(uintptr(tid), wmQuit, 0, 0)
	}

	select {
	case <-b.doneCh:
	case <-time.After(joinTimeout):
		// Pump did not exit in time. Surface nothing: there is no logger
		// wired into the backend, and hanging shutdown would be worse. The
		// caller still returns, so Stop never blocks indefinitely.
	}
}
