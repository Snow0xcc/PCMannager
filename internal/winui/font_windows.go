//go:build windows

package winui

import (
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// 字体枚举入口只有 EnumFontFamiliesExW，位于 gdi32。与 draw_windows.go 一样，
// 特性自用的 proc 就近声明。
var procEnumFontFamiliesExW = gdi32.NewProc("EnumFontFamiliesExW")

// RASTER_FONTTYPE 是 EnumFontFamiliesExW 回调里 fontType 的最低位，标记光栅
// （点阵）字体。这类字体是为固定字号画出来的位图，当界面字体用会糊，枚举时跳过。
const rasterFontType = 1

// enumFontFamiliesAll 是 EnumFontFamiliesExW 的 dwFlags 取值。该参数在 API 中
// 是保留位，Windows 自身忽略其内容，取值不影响枚举结果；按约定传 DWORD 的 -1。
const enumFontFamiliesAll = 0xFFFFFFFF

// textMetricW mirrors the Win32 TEXTMETRICW. The enumeration callback receives a
// pointer to it, so this declaration exists mainly to keep the callback signature
// honest; it is spelled out in full rather than faked with an opaque byte block
// because Go's field layout rules match the C one here: 11 LONG (44 bytes), four
// WCHAR (8 bytes) and five BYTE (5 bytes) come to 57 bytes, which the 4-byte
// alignment of LONG rounds up to the same 60 bytes sizeof(TEXTMETRICW) reports.
// A short placeholder would be the dangerous kind of wrong the moment anyone
// dereferenced it, and the field names document what the callback is handed.
type textMetricW struct {
	Height           int32
	Ascent           int32
	Descent          int32
	InternalLeading  int32
	ExternalLeading  int32
	AveCharWidth     int32
	MaxCharWidth     int32
	Weight           int32
	Overhang         int32
	DigitizedAspectX int32
	DigitizedAspectY int32
	FirstChar        uint16
	LastChar         uint16
	DefaultChar      uint16
	BreakChar        uint16
	Italic           byte
	Underlined       byte
	StruckOut        byte
	PitchAndFamily   byte
	CharSet          byte
}

// fontCache memoises the enumeration.
//
// The set of installed families does not change while the process runs (adding a
// font needs an install, which is not something this tray app does), so paying for
// a full GDI enumeration on every call to populate a picker would be wasteful. A
// sync.Once also makes the "call it from any goroutine" case safe.
var (
	fontOnce sync.Once
	fontList []string
)

// FontFamilies returns the installed font family names, sorted and de-duplicated.
// It is used to populate a font picker, so a stable order matters.
// Returns nil when enumeration is unavailable.
func FontFamilies() []string {
	fontOnce.Do(func() { fontList = enumerateFontFamilies() })
	// Hand back a copy: callers (a picker) may sort or trim the slice, and the
	// cache must not be mutated behind the next caller's back.
	return append([]string(nil), fontList...)
}

// enumerateFontFamilies walks the installed families once and returns the sorted,
// de-duplicated names.
func enumerateFontFamilies() []string {
	dc, _, _ := procGetDC.Call(0)
	if dc == 0 {
		return nil
	}
	defer procReleaseDC.Call(0, dc)

	// DEFAULT_CHARSET + 空 FaceName 是 "枚举所有字体族" 的约定：指定具体字符集
	// 反而只返回该字符集下的字体。CharSet 为空（0 = ANSI_CHARSET）会漏掉纯
	// Unicode（如中文字体）家族。
	lf := LOGFONTW{CharSet: DEFAULT_CHARSET}

	var out []string
	seen := make(map[string]struct{}, 64)

	cb := syscall.NewCallback(func(face *LOGFONTW, _ *textMetricW, fontType uint32, _ uintptr) uintptr {
		if fontType&rasterFontType != 0 {
			return 1 // 跳过点阵字体，继续枚举
		}
		name := syscall.UTF16ToString(face.FaceName[:])
		if name == "" {
			return 1
		}
		// 竖排字体的族名以 '@' 开头（如 @微软雅黑），它们是同名字体的镜像版本，
		// 给选择框用只会制造一堆看不懂的条目。
		if strings.HasPrefix(name, "@") {
			return 1
		}
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			out = append(out, name)
		}
		return 1 // 非 0 => 继续枚举
	})

	// lParam 留给回调传参；这里用闭包捕获 out/seen，故传 0。
	procEnumFontFamiliesExW.Call(dc, uintptr(unsafe.Pointer(&lf)), cb, 0, enumFontFamiliesAll)
	// 回调由 GDI 在枚举期间同步调用，必须确保其函数指针在 Call 返回前一直有效。
	runtime.KeepAlive(cb)
	runtime.KeepAlive(&lf)

	sort.Strings(out)
	return out
}
