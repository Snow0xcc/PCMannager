//go:build !windows

package winui

// InputDialog 非 Windows 下不支持：无原生窗口层，恒返回取消。
func InputDialog(owner HWND, title, label, initial string) (string, bool) {
	return "", false
}
