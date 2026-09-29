//go:build windows

package winui

import (
	"sync"
	"syscall"
	"unsafe"
)

// 弹层菜单 owner-draw 支持：Win32 菜单不支持图文混排，带图标的项必须用
// MF_OWNERDRAW 追加，系统随后向属主窗口发 WM_MEASUREITEM（量尺寸）与
// WM_DRAWITEM（画内容）。属主窗口在 wndProc 里调用 DispatchOwnerDraw 即可
// 完成接入（参照 launcher 面板的用法）。
const (
	wmDrawItem    = 0x002B
	wmMeasureItem = 0x002C

	odsSelected = 0x0001
	odsGrayed   = 0x0004

	// GetSysColor 索引：菜单背景/文字/高亮。
	colorMenu          = 4
	colorMenuText      = 7
	colorHighlight     = 13
	colorHighlightText = 14
	colorGrayText      = 17

	// owner-draw 项的排版尺寸（菜单图标 16px 方框 + 间距）。
	odIconSide   = 16
	odIconPad    = 8
	odTextGap    = 26 // 文字左缘（含图标区）
	odRightPad   = 14
	odTextPadV   = 7
	odMinHeight  = 22
	odDisabledHi = uintptr(1) << 31 // itemData 的禁用标记位
)

// MenuItem 是弹层菜单的一项。Separator 为 true 时其它字段忽略。
type MenuItem struct {
	Text      string
	Checked   bool
	Separator bool
	// Icon 是可选的图标绘制回调：在菜单项左侧 odIconSide 见方的方框内绘制，
	// textColor 建议用文字色（选中态自动切高亮文字色）。带 Icon 的项走
	// owner-draw 路径（图标+文字整体自绘）；其余项走系统 MF_STRING。
	Icon func(c *Canvas, box Rect, textColor uint32)
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
	RcItem     Rect
	ItemData   uintptr
}

// ownerDrawRegistry 是带图标菜单项的注册表：dwItemData 的 key → 图标回调。
// PopupMenu 弹出前注册、返回后清理；key 是自增计数（非指针），菜单存续期间
// 注册表持有内容副本，无悬挂风险。
var ownerDrawRegistry = struct {
	sync.RWMutex
	m    map[uintptr]MenuItem
	next uintptr
}{m: map[uintptr]MenuItem{}}

func odPut(item MenuItem) uintptr {
	ownerDrawRegistry.Lock()
	defer ownerDrawRegistry.Unlock()
	ownerDrawRegistry.next++
	ownerDrawRegistry.m[ownerDrawRegistry.next] = item
	return ownerDrawRegistry.next
}

func odGet(key uintptr) (MenuItem, bool) {
	ownerDrawRegistry.RLock()
	defer ownerDrawRegistry.RUnlock()
	it, ok := ownerDrawRegistry.m[key&^odDisabledHi]
	return it, ok
}

func odClearAll() {
	ownerDrawRegistry.Lock()
	ownerDrawRegistry.m = map[uintptr]MenuItem{}
	ownerDrawRegistry.Unlock()
}

// PopupMenu 在指定屏幕坐标弹出上下文菜单，阻塞直到用户选择或取消。返回被
// 选中项在 items 中的下标（0 起，分隔符不计入），取消/未选择返回 -1。
//
// 注意：菜单是瞬态的，函数返回时已销毁，调用方必须立刻根据下标行动。
// 属主窗口的 wndProc 需把 WM_MEASUREITEM/WM_DRAWITEM 交给 DispatchOwnerDraw
// （仅当菜单带图标项时必要）。
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
	hasOwnerDraw := false
	for i, it := range items {
		if it.Separator {
			procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
			continue
		}
		cmd++
		idxByCmd[cmd] = i
		if it.Icon != nil {
			// 带图标：owner-draw 路径，dwItemData 传注册表 key。
			hasOwnerDraw = true
			key := odPut(it)
			procAppendMenuW.Call(hMenu, mfOwnerDraw, uintptr(cmd), key)
			continue
		}
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

	if hasOwnerDraw {
		odClearAll() // 菜单已关，释放全部图标注册
	}
	if r == 0 {
		return -1
	}
	if i, ok := idxByCmd[uint32(r)]; ok {
		return i
	}
	return -1
}

// mfOwnerDraw 是 AppendMenuW 的 MF_OWNERDRAW（0x10B 含 MF_STRING|MF_BYCOMMAND 位）。
const mfOwnerDraw = 0x0000010B

// procGetSysColor 返回系统配色（菜单背景/文字/高亮），user32 导出。
var procGetSysColor = user32.NewProc("GetSysColor")

// DispatchOwnerDraw 处理属主窗口收到的 WM_MEASUREITEM/WM_DRAWITEM，返回
// (结果, 已处理)。属主 wndProc 在消息入口处调用：
//
//	if res, handled := winui.DispatchOwnerDraw(msg, wParam, lParam); handled {
//	    return res, true
//	}
func DispatchOwnerDraw(msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case wmMeasureItem:
		if wParam != 0 {
			return 0, false
		}
		mis := (*measureItemStruct)(lParamPtr[measureItemStruct](lParam))
		if mis == nil {
			return 0, false
		}
		it, ok := odGet(mis.ItemData)
		if !ok || it.Icon == nil {
			return 0, false
		}
		w, h := odMeasureText(it.Text)
		mis.ItemWidth = uint32(w)
		mis.ItemHeight = uint32(h)
		return 1, true

	case wmDrawItem:
		if wParam != 0 {
			return 0, false
		}
		dis := (*drawItemStruct)(lParamPtr[drawItemStruct](lParam))
		if dis == nil {
			return 0, false
		}
		it, ok := odGet(dis.ItemData)
		if !ok || it.Icon == nil {
			return 0, false
		}
		odPaintItem(dis, it)
		return 1, true
	}
	return 0, false
}

// lParamPtr restores the pointer the system delivered inside a message
// parameter (vet-clean two-hop form; see internal/tray/ownerdraw_windows.go).
func lParamPtr[T any](lParam uintptr) *T {
	return (*T)(*(*unsafe.Pointer)(unsafe.Pointer(&lParam)))
}

// odMeasureText 量一行菜单文字的尺寸（用默认 GUI 字体，与 shell 菜单最接近）。
func odMeasureText(s string) (int32, int32) {
	width := int32(odTextGap + odRightPad)
	height := int32(odMinHeight)
	if s == "" {
		return width, height
	}
	dc, _, _ := procGetDC.Call(0)
	if dc == 0 {
		return width, height
	}
	defer procReleaseDC.Call(0, dc)
	f, _, _ := procGetStockObject.Call(17 /*DEFAULT_GUI_FONT*/)
	old, _, _ := procSelectObject.Call(dc, f)
	defer procSelectObject.Call(dc, old)

	var sz struct{ CX, CY int32 }
	n := len(syscall.StringToUTF16(s)) - 1
	procGetTextExtentPoint32W.Call(dc, uintptr(unsafe.Pointer(mustUTF16(s))), uintptr(n), uintptr(unsafe.Pointer(&sz)))
	if w := sz.CX + odTextGap + odRightPad; w > width {
		width = w
	}
	if h := sz.CY + odTextPadV; h > height {
		height = h
	}
	return width, height
}

// odPaintItem 画一行 owner-draw 菜单项：背景（高亮态切换）、图标、文字。
func odPaintItem(dis *drawItemStruct, it MenuItem) {
	selected := dis.ItemState&odsSelected != 0
	grayed := dis.ItemState&odsGrayed != 0

	// 背景：选中用 COLOR_HIGHLIGHT，其余 COLOR_MENU。owner-draw 必须自画背景。
	bgIdx := uintptr(colorMenu)
	if selected {
		bgIdx = colorHighlight
	}
	rc := dis.RcItem
	if brush, _, _ := procGetSysColorBrush.Call(bgIdx); brush != 0 {
		procFillRect.Call(dis.HDC, uintptr(unsafe.Pointer(&rc)), brush)
	}

	// 文字色：选中用高亮文字，禁用用灰字，其余用菜单文字色。
	fgIdx := uintptr(colorMenuText)
	switch {
	case selected:
		fgIdx = colorHighlightText
	case grayed:
		fgIdx = colorGrayText
	}
	textColor, _, _ := procGetSysColor.Call(fgIdx)

	// 图标：左侧 odIconSide 方框，垂直居中。
	iconBox := Rect{
		Left:   rc.Left + odIconPad,
		Top:    rc.Top + (rc.Height()-odIconSide)/2,
		Right:  rc.Left + odIconPad + odIconSide,
		Bottom: rc.Top + (rc.Height()-odIconSide)/2 + odIconSide,
	}
	c := &Canvas{hdc: dis.HDC}
	it.Icon(c, iconBox, uint32(textColor))

	// 文字：图标区右侧，垂直居中，超长截断。
	textRect := rc
	textRect.Left = rc.Left + odTextGap
	p, err := syscall.UTF16PtrFromString(it.Text)
	if err != nil {
		return
	}
	procSetBkMode.Call(dis.HDC, TRANSPARENT)
	procSetTextColor.Call(dis.HDC, textColor)
	f, _, _ := procGetStockObject.Call(17)
	old, _, _ := procSelectObject.Call(dis.HDC, f)
	defer procSelectObject.Call(dis.HDC, old)
	const flags = DT_LEFT | DT_SINGLELINE | DT_VCENTER | DT_NOPREFIX | DT_END_ELLIPSIS
	procDrawTextW.Call(dis.HDC, uintptr(unsafe.Pointer(p)), ^uintptr(0),
		uintptr(unsafe.Pointer(&textRect)), flags)
}
