//go:build windows

package winui

import (
	"syscall"
	"unsafe"
)

// MenuItem 是弹层菜单的一项。Separator 为 true 时其它字段忽略。
type MenuItem struct {
	Text      string
	Checked   bool
	Separator bool
}

// PopupMenu 在指定屏幕坐标弹出上下文菜单，阻塞直到用户选择或取消。返回被
// 选中项在 items 中的下标（0 起，分隔符不计入），取消/未选择返回 -1。
//
// 注意：菜单是瞬态的，函数返回时已销毁，调用方必须立刻根据下标行动。
func PopupMenu(owner HWND, x, y int32, items []MenuItem) int {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return -1
	}
	defer procDestroyMenu.Call(hMenu)

	// cmd 是 AppendMenuW 的菜单命令号；TPM_RETURNCMD 会把它作为返回值带回。
	// 分隔符占位但无命令号，故维护 cmd → 原始下标 的映射。
	idxByCmd := map[uint32]int{}
	var cmd uint32
	for i, it := range items {
		if it.Separator {
			procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
			continue
		}
		cmd++
		idxByCmd[cmd] = i
		flags := uintptr(MF_STRING)
		if it.Checked {
			flags |= MF_CHECKED
		}
		p, err := syscall.UTF16PtrFromString(it.Text)
		if err != nil {
			continue
		}
		procAppendMenuW.Call(hMenu, flags, uintptr(cmd), uintptr(unsafe.Pointer(p)))
	}

	// 菜单弹出前先把前台权交回属主窗口，否则点菜单外区域后菜单不关（Win32
	// 菜单模态的经典坑）。
	procSetForegroundWindow.Call(uintptr(owner))
	r, _, _ := procTrackPopupMenu.Call(hMenu,
		TPM_RIGHTBUTTON|TPM_RETURNCMD,
		uintptr(x), uintptr(y), 0, uintptr(owner), 0)

	if r == 0 {
		return -1
	}
	if i, ok := idxByCmd[uint32(r)]; ok {
		return i
	}
	return -1
}
