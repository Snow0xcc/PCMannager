//go:build windows

package tray

import (
	"log/slog"
	"sync"
	"syscall"
	"unsafe"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// Menu command ids are offset so they never collide with the reserved range.
const commandBase = 1000

// winTray owns a hidden message window that hosts the notification icon.
type winTray struct {
	log     *slog.Logger
	handler Handler

	mu     sync.Mutex
	window *winui.Window
	nid    winui.NOTIFYICONDATAW
	menu   Menu
	// commands maps a menu command id back to a stable item ID.
	commands map[uint32]string
	added    bool
	visible  bool
}

// New creates a Windows tray icon bound to the given handler.
func New(log *slog.Logger, handler Handler) Tray {
	return &winTray{log: log, handler: handler, commands: map[uint32]string{}}
}

// SetMenu replaces the tray menu and refreshes the tooltip.
func (t *winTray) SetMenu(m Menu) {
	t.mu.Lock()
	t.menu = m
	t.mu.Unlock()

	if t.nid.HWnd.Valid() {
		t.updateTooltip(m.Tooltip)
	}
}

// Show makes the icon visible, creating the message window on first use.
func (t *winTray) Show() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.added {
		return nil
	}
	if err := t.createWindowLocked(); err != nil {
		return err
	}

	t.nid = winui.NOTIFYICONDATAW{
		Size:     uint32(unsafe.Sizeof(winui.NOTIFYICONDATAW{})),
		HWnd:     t.window.HWND(),
		ID:       1,
		Flags:    winui.NIF_MESSAGE | winui.NIF_ICON | winui.NIF_TIP,
		Callback: winui.WM_TRAYCALLBACK,
		Icon:     defaultIcon(),
	}
	copyTip(&t.nid, t.menu.Tooltip)

	if err := t.shellNotify(winui.NIM_ADD); err != nil {
		return err
	}
	t.added = true
	t.visible = true
	return nil
}

// Hide removes the icon from the notification area.
func (t *winTray) Hide() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.added {
		return
	}
	_ = t.shellNotify(winui.NIM_DELETE)
	t.added = false
	t.visible = false
}

// Visible reports whether the icon is currently shown.
func (t *winTray) Visible() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.visible
}

// Destroy removes the icon and releases the message window.
func (t *winTray) Destroy() {
	t.Hide()
	t.mu.Lock()
	w := t.window
	t.window = nil
	t.mu.Unlock()
	if w != nil {
		w.Destroy()
	}
}

// createWindowLocked builds the hidden message-only window. Caller holds mu.
func (t *winTray) createWindowLocked() error {
	if t.window != nil {
		return nil
	}
	w, err := winui.NewWindow("GoBoxTray", 0, 0, 0)
	if err != nil {
		return err
	}
	w.Handle = t.wndProc
	t.window = w
	return nil
}

// wndProc dispatches tray callbacks and menu commands.
func (t *winTray) wndProc(hwnd winui.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case winui.WM_TRAYCALLBACK:
		// lParam carries the mouse event for our icon.
		switch uint32(lParam) {
		case winui.WM_RBUTTONUP, winui.WM_CONTEXTMENU:
			t.popupMenu()
		case winui.WM_LBUTTONDBLCLK:
			// Double-click is a convenience for "open the panel".
			if t.handler != nil {
				t.handler.OnSelect("open_panel")
			}
		}
		return 0, true

	case winui.WM_SETTINGCHANGE, winui.WM_DISPLAYCHANGE:
		// The notification area may have been recreated; re-add the icon.
		t.reAdd()
		return 0, true

	case winui.WM_DESTROY:
		t.Hide()
		return 0, true
	}

	// Menu commands arrive as WM_COMMAND with the id in the low word of wParam.
	const wmCommand = 0x0111
	if msg == wmCommand {
		cmd := uint32(wParam & 0xFFFF)
		t.mu.Lock()
		id, ok := t.commands[cmd]
		t.mu.Unlock()
		if ok && t.handler != nil {
			t.handler.OnSelect(id)
		}
		return 0, true
	}
	return 0, false
}

// popupMenu builds and shows the context menu at the cursor.
func (t *winTray) popupMenu() {
	t.mu.Lock()
	menu := t.menu
	t.mu.Unlock()

	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	commands := map[uint32]string{}
	var cmd uint32 = commandBase

	for _, it := range menu.Items {
		if it.Separator {
			procAppendMenuW.Call(hMenu, winui.MF_SEPARATOR, 0, 0)
			continue
		}
		flags := uintptr(winui.MF_STRING)
		if it.Disabled {
			flags |= winui.MF_GRAYED
		}
		if it.Checkable && it.Checked {
			flags |= winui.MF_CHECKED
		}
		title, err := syscall.UTF16PtrFromString(it.Title)
		if err != nil {
			continue
		}
		procAppendMenuW.Call(hMenu, flags, uintptr(cmd), uintptr(unsafe.Pointer(title)))
		commands[cmd] = it.ID
		cmd++
	}

	t.mu.Lock()
	t.commands = commands
	hwnd := winui.Invalid
	if t.window != nil {
		hwnd = t.window.HWND()
	}
	t.mu.Unlock()

	if !hwnd.Valid() {
		return
	}

	// Required so the menu closes when the user clicks elsewhere.
	procSetForegroundWindow.Call(uintptr(hwnd))

	pos := winui.CursorPos()
	r, _, _ := procTrackPopupMenu.Call(hMenu,
		winui.TPM_RIGHTBUTTON,
		uintptr(pos.X), uintptr(pos.Y), 0, uintptr(hwnd), 0)
	_ = r

	// Standard workaround: post a null message so the menu dismisses cleanly.
	winui.PostMessage(hwnd, 0, 0, 0)
}

// updateTooltip refreshes the hover text.
func (t *winTray) updateTooltip(tip string) {
	t.mu.Lock()
	copyTip(&t.nid, tip)
	t.mu.Unlock()
	_ = t.shellNotify(winui.NIM_MODIFY)
}

// reAdd re-registers the icon after Explorer restarts.
func (t *winTray) reAdd() {
	t.mu.Lock()
	wasAdded := t.added
	t.added = false
	t.mu.Unlock()
	if wasAdded {
		_ = t.Show()
	}
}

// shellNotify wraps Shell_NotifyIconW.
func (t *winTray) shellNotify(action uint32) error {
	t.mu.Lock()
	nid := t.nid
	t.mu.Unlock()
	if !nid.HWnd.Valid() {
		return nil
	}
	r, _, err := procShellNotifyIconW.Call(uintptr(action), uintptr(unsafe.Pointer(&nid)))
	if r == 0 {
		if err != nil && err != syscall.Errno(0) {
			return err
		}
		return errShellNotify
	}
	return nil
}

// copyTip writes a NUL-terminated tooltip into the fixed-size array.
func copyTip(nid *winui.NOTIFYICONDATAW, tip string) {
	for i := range nid.Tip {
		nid.Tip[i] = 0
	}
	if tip == "" {
		tip = "GoBox"
	}
	// Reserve the last slot for the NUL terminator.
	runes := []rune(tip)
	if len(runes) > len(nid.Tip)-1 {
		runes = runes[:len(nid.Tip)-1]
	}
	copy(nid.Tip[:], syscall.StringToUTF16(string(runes)))
}

// defaultIcon loads the application's own small icon.
func defaultIcon() uintptr {
	const (
		imageIcon     = 1
		lrDefaultSize = 0x0000
		lrShared      = 0x8000
	)
	h, _, _ := procLoadImageW.Call(0, 0, imageIcon, 0, 0, lrDefaultSize|lrShared)
	if h != 0 {
		return h
	}
	// Fall back to the generic application icon supplied by the shell.
	const idiApplication = 32512
	icon, _, _ := procLoadIconW.Call(0, idiApplication)
	return icon
}
