//go:build windows

package winui

import (
	"syscall"
	"unsafe"
)

// 输入对话框用的 Win32 常量（就近声明，避免往 api_windows.go 塞一次性常量）。
const (
	WS_OVERLAPPED  = 0x00000000
	WS_CAPTION     = 0x00C00000
	WS_SYSMENU     = 0x00080000
	WS_MINIMIZEBOX = 0x00020000
	WS_MAXIMIZEBOX = 0x00010000
	WS_GROUP       = 0x00020000
	WS_TABSTOP     = 0x00010000

	WS_EX_DLGMODALFRAME = 0x00000001

	ES_AUTOHSCROLL = 0x0080

	BN_CLICKED = 0

	// 控件命令号（WM_COMMAND 的 ctlID）。
	idcEdit    = 101
	idcOK      = 1 // IDOK
	idcCancel  = 2 // IDCANCEL
	wmCommand  = 0x0111
	wmSetFocus = 0x0007
)

// inputDialogState 是模态输入对话框的窗口线程状态。
type inputDialogState struct {
	edit HWND
	// done 关闭模态循环；ok 记录退出方式（确定 = true）。
	done chan struct{}
	ok   bool
}

// InputDialog 弹出一个模态输入框（标题 title、提示 label、初始值 initial），
// 阻塞直到用户确定或取消。返回 (输入值, 是否确定)。
//
// 必须在窗口所属线程上调用（即已在 LockOSThread 的 goroutine 里）；它内部跑
// 嵌套消息循环，是 Win32 模态对话框的标准做法。任何创建失败返回 ("", false)。
func InputDialog(owner HWND, title, label, initial string) (string, bool) {
	d := &inputDialogState{done: make(chan struct{})}

	win, err := NewWindow("GoBoxInputDialog",
		WS_OVERLAPPED|WS_CAPTION|WS_SYSMENU|WS_MINIMIZEBOX,
		WS_EX_DLGMODALFRAME, Invalid)
	if err != nil {
		return "", false
	}
	win.Handle = func(hwnd HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
		return d.proc(win, hwnd, msg, wParam, lParam)
	}

	// 布局（客户区 360x110）：标签 + 编辑框 + 确定/取消。
	sw, sh := ScreenSize()
	const (
		w, h   = 360, 130
		pad    = 12
		editH  = 24
		btnW   = 80
		btnH   = 26
		btnRow = h - btnH - 12
	)
	x := (sw - w) / 2
	y := (sh - h) / 3
	MoveWindow(win.HWND(), int32(x), int32(y), w, h, false)
	SetWindowTextH(win.HWND(), title)

	inst := getModuleHandle()

	// STATIC 标签。
	lp, _ := syscall.UTF16PtrFromString(label)
	hLabel, _, _ := procCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(mustUTF16("STATIC"))),
		uintptr(unsafe.Pointer(lp)),
		uintptr(WS_CHILD|WS_VISIBLE|WS_GROUP),
		pad, pad, w-pad*2, 18,
		uintptr(win.HWND()), 0, inst, 0)
	_ = hLabel

	// EDIT 输入框。
	ep, _ := syscall.UTF16PtrFromString(initial)
	hEdit, _, _ := procCreateWindowExW.Call(
		WS_EX_CLIENTEDGE,
		uintptr(unsafe.Pointer(mustUTF16("EDIT"))),
		uintptr(unsafe.Pointer(ep)),
		uintptr(WS_CHILD|WS_VISIBLE|WS_TABSTOP|ES_AUTOHSCROLL),
		pad, pad+22, w-pad*2, editH,
		uintptr(win.HWND()), uintptr(idcEdit), inst, 0)
	d.edit = HWND(hEdit)

	// 按钮。
	btn := func(text string, id uintptr, bx int32) {
		tp, _ := syscall.UTF16PtrFromString(text)
		procCreateWindowExW.Call(0,
			uintptr(unsafe.Pointer(mustUTF16("BUTTON"))),
			uintptr(unsafe.Pointer(tp)),
			uintptr(WS_CHILD|WS_VISIBLE|WS_TABSTOP),
			uintptr(bx), uintptr(btnRow), uintptr(btnW), uintptr(btnH),
			uintptr(win.HWND()), id, inst, 0)
	}
	btn("确定", idcOK, w-btnW*2-pad*2)
	btn("取消", idcCancel, w-btnW-pad)

	// 默认焦点给编辑框并全选（方便直接输入覆盖）。
	procSetFocus.Call(hEdit)
	PostMessage(win.HWND(), wmSetSelectAll, 0, 0)

	ShowWindow(win.HWND(), SW_SHOW)
	SetForegroundWindow(win.HWND())

	// 嵌套模态消息循环：WM_QUIT 也要转发（保持宿主线程循环语义）。
	quitCode := int32(-1)
	var msg MSG
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			quitCode = int32(r)
			break
		}
		// Tab 导航与回车/Esc 由 IsDialogMessage 处理（它分发已处理的消息）。
		if d.handleDialogKeys(win.HWND(), &msg) {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		select {
		case <-d.done:
			goto out
		default:
		}
	}
out:
	win.Destroy()
	if quitCode >= 0 {
		PostQuitMessage(quitCode)
	}
	if !d.ok {
		return "", false
	}
	return WindowText(d.edit), true
}

// wmSetSelectAll 是发给输入框的自定义消息（EM_SETSEL 全选的包装）。
const wmSetSelectAll = 0x0400 + 100 // WM_USER+100

// handleDialogKeys 用 IsDialogMessage 让 Tab/回车/Esc 在对话框内正确工作。
// 未导出 proc 就地绑定（一次性使用）。
var procIsDialogMessage = user32.NewProc("IsDialogMessageW")

func (d *inputDialogState) handleDialogKeys(h HWND, msg *MSG) bool {
	r, _, _ := procIsDialogMessage.Call(uintptr(h), uintptr(unsafe.Pointer(msg)))
	return r != 0
}

// proc 是输入对话框的消息处理。
func (d *inputDialogState) proc(win *Window, hwnd HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case wmSetSelectAll:
		if d.edit.Valid() {
			sendEditSelectAll(d.edit)
		}
		return 0, true
	case wmCommand:
		// HIWORD=通知码，LOWORD=控件 ID。
		switch wParam & 0xFFFF {
		case idcOK:
			d.ok = true
			d.finish()
			return 0, true
		case idcCancel:
			d.ok = false
			d.finish()
			return 0, true
		}
	case WM_CLOSE:
		d.ok = false
		d.finish()
		return 0, true
	case WM_DESTROY:
		return 0, true
	}
	return 0, false
}

// finish 结束模态循环（幂等）。
func (d *inputDialogState) finish() {
	select {
	case <-d.done:
	default:
		close(d.done)
	}
}

// sendEditSelectAll 给编辑框发 EM_SETSEL(0,-1) 全选。
func sendEditSelectAll(h HWND) {
	const emSetSel = 0x00B1
	SendMessage(h, emSetSel, 0, ^uintptr(0))
}

// SetWindowTextH 设置窗口标题文本（对话框标题用）。
func SetWindowTextH(h HWND, text string) {
	p, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	const wmSetText = 0x000C
	SendMessage(h, wmSetText, 0, uintptr(unsafe.Pointer(p)))
}

// mustUTF16 转换字符串，失败时返回空串指针（控件类名不会失败）。
func mustUTF16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// WS_EX_CLIENTEDGE 与 procSetFocus 就近声明。
const (
	WS_EX_CLIENTEDGE = 0x00000200
)

var procSetFocus = user32.NewProc("SetFocus")
