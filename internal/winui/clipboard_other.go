//go:build !windows

package winui

// ClipboardFileDrop 非 Windows 下不支持：CF_HDROP 是 Windows 剪贴板专有格式，
// 其它平台的等价能力（如 X11 的 text/uri-list）语义不同，这里不做模拟。
func ClipboardFileDrop(paths []string) error { return errUnsupported }
