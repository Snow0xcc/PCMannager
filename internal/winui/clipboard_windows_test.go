//go:build windows

package winui

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// 测试内自行绑定 DragQueryFileW（shell32）与 GetClipboardData（user32）：
// 二者只用于验证剪贴板往返，不进入生产 surface。挂错 DLL 会在首次调用时
// panic，故这里的 DLL 归属同样要正确。
var (
	procDragQueryFileW   = shell32.NewProc("DragQueryFileW")
	procGetClipboardData = user32.NewProc("GetClipboardData")
)

// openClipboardForRead 打开剪贴板用于读取。无交互桌面等环境原因导致打不开时
// 跳过测试而非判失败——那属于环境不支持，不是代码缺陷。
func openClipboardForRead(t *testing.T) {
	t.Helper()
	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		t.Skip("OpenClipboard 失败（可能无交互桌面），跳过剪贴板往返测试")
	}
	t.Cleanup(func() { procCloseClipboard.Call() })
}

// TestClipboardFileDropRoundTrip 用真实 Win32 往返验证：写入 CF_HDROP 后，
// 通过 DragQueryFileW 读回的路径必须与放入的绝对路径完全一致。
func TestClipboardFileDropRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "pcmannager-clipboard.txt")
	if err := os.WriteFile(want, []byte("hello"), 0o600); err != nil {
		t.Fatalf("写临时文件失败: %v", err)
	}

	if err := ClipboardFileDrop([]string{want}); err != nil {
		if strings.Contains(err.Error(), "打开剪贴板失败") {
			t.Skipf("剪贴板不可用（可能无交互桌面）: %v", err)
		}
		t.Fatalf("ClipboardFileDrop 失败: %v", err)
	}

	openClipboardForRead(t)

	hDrop, _, _ := procGetClipboardData.Call(CF_HDROP)
	if hDrop == 0 {
		t.Fatal("剪贴板中没有 CF_HDROP 数据")
	}

	// iFile = 0xFFFFFFFF 是 DragQueryFileW 查询条目数的约定。
	n, _, _ := procDragQueryFileW.Call(hDrop, uintptr(0xFFFFFFFF), 0, 0)
	if n != 1 {
		t.Fatalf("DragQueryFileW 报告 %d 个文件，期望 1", n)
	}

	buf := make([]uint16, 1024)
	got, _, _ := procDragQueryFileW.Call(hDrop, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if got == 0 {
		t.Fatal("DragQueryFileW 读取路径失败")
	}
	if path := syscall.UTF16ToString(buf[:got]); path != want {
		t.Fatalf("读回路径 = %q, 期望 %q", path, want)
	}
}

// TestClipboardFileDropRejectsInvalidPaths 覆盖无需触碰剪贴板即可拒绝的无效
// 输入：空列表与相对路径都不应被静默写入。
func TestClipboardFileDropRejectsInvalidPaths(t *testing.T) {
	if err := ClipboardFileDrop(nil); err == nil {
		t.Fatal("空路径列表应返回错误")
	}
	if err := ClipboardFileDrop([]string{"relative\\path.txt"}); err == nil {
		t.Fatal("相对路径应返回错误")
	}
}
