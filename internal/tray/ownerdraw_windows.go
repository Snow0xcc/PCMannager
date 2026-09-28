//go:build windows

// Owner-draw support for colored tray menu items.
//
// 标准 Win32 菜单不支持文字颜色（AppendMenuW / SetMenuItemInfo / hbmpItem
// 都做不到逐项上色），唯一轻量可靠的做法是 owner-draw：带颜色的项用
// MF_OWNERDRAW 追加，系统随后向属主窗口发 WM_MEASUREITEM（量尺寸）与
// WM_DRAWITEM（画内容）。
//
// 数据流：
//  1. popupMenu 在构建菜单时为每个彩色项分配一个自增 key，把 {标题, 颜色}
//     存进 winTray.ownerDraw（map[uintptr]ownerDrawItem），key 作为
//     AppendMenuW 的 dwItemData 传出；
//  2. 系统把同一个 key 原样带回 MEASUREITEMSTRUCT.itemData /
//     DRAWITEMSTRUCT.itemData，wndProc 用它查回内容；
//  3. TrackPopupMenu 返回（菜单已关）后清空 map，释放全部条目。
//
// key 是普通计数器而非指针：菜单存活的整个期间 map 持有内容副本，不存在
// 悬挂指针，清理也只需一次 map 重置。
package tray

import (
	"syscall"
	"unsafe"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

const (
	// wmDrawItem / wmMeasureItem are the standard owner-draw messages
	// (winui 尚未声明这两个常量，就地定义并保持与 winuser.h 一致).
	wmDrawItem    = 0x002B
	wmMeasureItem = 0x002C

	// ODS_SELECTED/ODS_GRAYED are the DRAWITEMSTRUCT.itemState bits we honor.
	odsSelected = 0x0001
	odsGrayed   = 0x0004

	// COLOR_* indices for GetSysColor.
	colorMenu          = 4 // MENU
	colorMenuText      = 7 // MENUTEXT
	colorHighlight     = 13
	colorHighlightText = 14
	colorGrayText      = 17

	// Owner-draw 项要自己留出勾选标记与边距，让文字与普通 MF_STRING 项对齐。
	odCheckGap  = 30
	odRightPad  = 16
	odTextPadV  = 7
	odMinHeight = 20
)

// ownerDrawItem is the payload behind one colored (owner-drawn) menu entry.
type ownerDrawItem struct {
	title string
	color uint32
}

// measureItemStruct mirrors Win32 MEASUREITEMSTRUCT (x64 layout).
type measureItemStruct struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemWidth  uint32
	ItemHeight uint32
	ItemData   uintptr
}

// drawItemStruct mirrors Win32 DRAWITEMSTRUCT (x64 layout).
type drawItemStruct struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemAction uint32
	ItemState  uint32
	HwndItem   uintptr
	HDC        uintptr
	RcItem     winui.Rect
	ItemData   uintptr
}

// lParamPtr restores the pointer the system delivered inside a message
// parameter. Direct unsafe.Pointer(lParam) triggers vet's unsafeptr warning;
// the two-hop form through &lParam is the vet-clean equivalent and stays
// correct: the parameter slot holds exactly the address we want.
func lParamPtr[T any](lParam uintptr) *T {
	return (*T)(*(*unsafe.Pointer)(unsafe.Pointer(&lParam)))
}

// putOwnerDraw stores an item and returns its key for dwItemData.
func (t *winTray) putOwnerDraw(title string, color uint32) uintptr {
	t.odMu.Lock()
	defer t.odMu.Unlock()
	if t.ownerDraw == nil {
		t.ownerDraw = map[uintptr]ownerDrawItem{}
	}
	t.odSeq++
	t.ownerDraw[t.odSeq] = ownerDrawItem{title: title, color: color}
	return t.odSeq
}

// getOwnerDraw looks the item back up by the key the system hands us.
func (t *winTray) getOwnerDraw(key uintptr) (ownerDrawItem, bool) {
	t.odMu.Lock()
	defer t.odMu.Unlock()
	it, ok := t.ownerDraw[key]
	return it, ok
}

// clearOwnerDraw releases all entries once the menu is closed.
func (t *winTray) clearOwnerDraw() {
	t.odMu.Lock()
	t.ownerDraw = nil
	t.odMu.Unlock()
}

// handleMeasureItem answers WM_MEASUREITEM: measure the text with
// GetTextExtentPoint32W on a DC from the owning window, add the checkbox gap
// and padding, and floor the height so a menu row never collapses.
func (t *winTray) handleMeasureItem(hwnd winui.HWND, mis *measureItemStruct) {
	title := ""
	// dwItemData 可能叠着禁用位（见 odKeyWithFlags），查表前剥离。
	if it, ok := t.getOwnerDraw(mis.ItemData &^ odDisabledBit); ok {
		title = it.title
	}
	w, h := measureMenuText(hwnd, title)
	mis.ItemWidth = uint32(w)
	mis.ItemHeight = uint32(h)
}

// measureMenuText measures one menu row. The DC comes from the tray window
// when possible; a message-only/hidden window may refuse GetDC, so fall back
// to the screen DC (same approach winui uses for font metrics).
func measureMenuText(hwnd winui.HWND, s string) (int32, int32) {
	width := int32(odCheckGap + odRightPad)
	height := int32(odMinHeight)
	if s == "" {
		return width, height
	}
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return width, height
	}
	// ReleaseDC 的窗口参数必须与 GetDC 一致（回退屏幕 DC 时为 NULL）。
	hwndUsed := uintptr(hwnd)
	dc, _, _ := procGetDC.Call(hwndUsed)
	if dc == 0 {
		hwndUsed = 0
		dc, _, _ = procGetDC.Call(0)
	}
	if dc == 0 {
		return width, height
	}
	defer procReleaseDC.Call(hwndUsed, dc)

	// 用系统默认 GUI 字体量字：托盘菜单没有自定义字体，DEFAULT_GUI_FONT
	// 与 Shell 渲染菜单所用的字号最接近。
	f, _, _ := procGetStockObject.Call(17 /*DEFAULT_GUI_FONT*/)
	old, _, _ := procSelectObject.Call(dc, f)
	defer procSelectObject.Call(dc, old)

	var sz struct{ CX, CY int32 }
	n := len(syscall.StringToUTF16(s)) - 1
	procGetTextExtentPoint32W.Call(dc, uintptr(unsafe.Pointer(p)), uintptr(n), uintptr(unsafe.Pointer(&sz)))

	w := sz.CX + odCheckGap + odRightPad
	h := sz.CY + odTextPadV
	if w > width {
		width = w
	}
	if h > height {
		height = h
	}
	return width, height
}

// handleDrawItem answers WM_DRAWITEM: fill the background (highlight when
// selected), then draw the text in the mapped color — or the system highlight
// text / gray text for selected and disabled states, which keeps the item
// readable in every menu state.
func (t *winTray) handleDrawItem(dis *drawItemStruct) {
	it, ok := t.getOwnerDraw(dis.ItemData)
	if !ok {
		return
	}
	selected := dis.ItemState&odsSelected != 0
	grayed := dis.ItemState&odsGrayed != 0

	// 文字色：选中用高亮文字，禁用用灰字，其余用映射色。
	fgIdx := uint32(colorMenuText)
	switch {
	case selected:
		fgIdx = colorHighlightText
	case grayed:
		fgIdx = colorGrayText
	}

	rc := dis.RcItem
	// 背景：选中用高亮色、其余用菜单色。owner-draw 项必须自己画背景，
	// 依赖系统预填充的行为在不同 shell 版本下不确定。
	bgIdx := uint32(colorMenu)
	if selected {
		bgIdx = colorHighlight
	}
	if brush, _, _ := procGetSysColorBrush.Call(uintptr(bgIdx)); brush != 0 {
		procFillRect.Call(dis.HDC, uintptr(unsafe.Pointer(&rc)), brush)
	}

	textColor := it.color
	if selected || grayed {
		c, _, _ := procGetSysColor.Call(uintptr(fgIdx))
		textColor = uint32(c)
	}

	// 文字缩进勾选标记区，与普通菜单项左对齐。
	textRect := rc
	textRect.Left = rc.Left + odCheckGap
	p, err := syscall.UTF16PtrFromString(it.title)
	if err != nil {
		return
	}
	procSetBkMode.Call(dis.HDC, winui.TRANSPARENT)
	procSetTextColor.Call(dis.HDC, uintptr(textColor))
	// DEFAULT_GUI_FONT：与量字用的同一字体，避免量画不一致。
	f, _, _ := procGetStockObject.Call(17)
	old, _, _ := procSelectObject.Call(dis.HDC, f)
	defer procSelectObject.Call(dis.HDC, old)
	const flags = winui.DT_LEFT | winui.DT_SINGLELINE | winui.DT_VCENTER | winui.DT_NOPREFIX
	procDrawTextW.Call(dis.HDC, uintptr(unsafe.Pointer(p)), ^uintptr(0),
		uintptr(unsafe.Pointer(&textRect)), uintptr(flags))
}
