//go:build windows

package clipboard

import (
	"context"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// pollInterval 与 pollEchoWindow 控制文件轮询的节奏与回声抑制窗口。
//
// golang.design/x/clipboard 的 Watch 只覆盖文本与位图，资源管理器“复制文件”
// 放上剪贴板的是 CF_HDROP（格式 15），Watch 通道收不到。这里用 800ms 轮询
// 补齐；之所以不用 AddClipboardFormatListener，是因为它需要在窗口过程中处理
// WM_CLIPBOARDUPDATE，而本模块的轮询器刻意不创建窗口（与 clip.Watch 并行、
// 随 ctx 退出），800ms 的延迟对“复制文件 → 入历史”的体感无影响。
const (
	pollInterval   = 800 * time.Millisecond
	pollEchoWindow = 3 * time.Second
)

var (
	// proc 名与 DLL 的绑定关系沿用 winui：剪贴板入口在 user32，DragQueryFileW
	// 在 shell32。挂错 DLL 不会在启动时报错，而是在首次调用时 panic。
	user32Clipboard      = syscall.NewLazyDLL("user32.dll")
	shell32Clipboard     = syscall.NewLazyDLL("shell32.dll")
	procGetClipboardData = user32Clipboard.NewProc("GetClipboardData")
	procOpenClipboard    = user32Clipboard.NewProc("OpenClipboard")
	procCloseClipboard   = user32Clipboard.NewProc("CloseClipboard")
	procDragQueryFileW   = shell32Clipboard.NewProc("DragQueryFileW")
)

// pollFiles watches the clipboard for CF_HDROP payloads until ctx is done and
// records the first dropped file as a file entry.
func (f *Feature) pollFiles(ctx context.Context) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.pollOnce()
		}
	}
}

// pollOnce samples the clipboard once: a CF_HDROP payload becomes one file
// entry, anything else is ignored here (the clip.Watch watcher owns text and
// images).
func (f *Feature) pollOnce() {
	path, ok := firstHDropFile()
	if !ok {
		return
	}
	if f.isEchoFile(path) {
		return
	}
	// 同一路径连续采样会重复命中：AddFileEntry 的相邻去重会把重复采样变成
	// 时间戳刷新，天然幂等。
	name := filepath.Base(path)
	f.hist.AddFileEntry(path, name)
	if f.ctx != nil {
		f.ctx.Logger.Debug("已记录剪贴板文件", "module", moduleID, "path", path)
	}
}

// firstHDropFile reads the clipboard's CF_HDROP payload and returns the first
// file's absolute path. It returns ok=false whenever the clipboard holds
// anything else, is briefly locked by another process, or errors out -- the
// poller simply retries on its next tick.
func firstHDropFile() (string, bool) {
	// OpenClipboard 是全局独占锁，短暂占用会失败；轮询语义下失败即跳过本轮，
	// 不做重试（与 winui 写入侧的重试策略不同：读侧错过 800ms 无副作用）。
	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		return "", false
	}
	defer procCloseClipboard.Call()

	hDrop, _, _ := procGetClipboardData.Call(winui.CF_HDROP)
	if hDrop == 0 {
		return "", false
	}
	// iFile = 0xFFFFFFFF 是 DragQueryFileW 查询条目数的约定。
	n, _, _ := procDragQueryFileW.Call(hDrop, uintptr(0xFFFFFFFF), 0, 0)
	if n == 0 {
		return "", false
	}
	// 多文件拖放只取第一个：历史条目按单条写回设计（写回也只放回第一个），
	// 其余文件忽略——这里不做多选历史，避免 Entry 语义膨胀。
	buf := make([]uint16, 1024)
	got, _, _ := procDragQueryFileW.Call(hDrop, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if got == 0 {
		return "", false
	}
	return syscall.UTF16ToString(buf[:got]), true
}
