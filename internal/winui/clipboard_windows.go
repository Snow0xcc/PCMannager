//go:build windows

package winui

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// droPFiles 对应 Win32 的 DROPFILES 头，必须位于 CF_HDROP 载荷的最前面。
// 它不是可省略的填充：Windows 与各家客户端都靠 pFiles 换算文件列表的起始
// 偏移，fWide 则决定列表是否为 UTF-16。
type droPFiles struct {
	PFiles uint32
	Pt     POINT
	FNC    int32
	FWide  int32
}

// clipboardRetries/clipboardDelay 限定等待剪贴板释放的次数与间隔。
//
// 剪贴板是一把全局独占锁：常见的短暂占用者（Office、剪贴板管理器）会让
// OpenClipboard 返回 ERROR_ACCESS_DENIED，但并非真的坏掉。立即放弃会把
// “复制文件”变成偶发失败，重试几次才符合它实际的状态。
const (
	clipboardRetries = 5
	clipboardDelay   = 20 * time.Millisecond
)

// ClipboardFileDrop puts the given files on the clipboard as a CF_HDROP file
// list, the format chat clients accept as "paste a file".
func ClipboardFileDrop(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("winui: 剪贴板文件列表不能为空")
	}
	// 直接拒绝相对路径：CF_HDROP 会被工作目录完全不同的其它进程消费，相对路径
	// 在那里指向别处，比在调用点报错更糟。
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("winui: 剪贴板文件路径不能为空")
		}
		if !filepath.IsAbs(p) {
			return fmt.Errorf("winui: 剪贴板文件必须是绝对路径: %s", p)
		}
	}

	// 先把整块载荷（DROPFILES 头 + 以 NUL 分隔的 UTF-16 路径 + 结尾的额外 NUL）
	// 组装好再碰剪贴板，这样组装失败不会留下一个被清空的剪贴板。
	headerSize := int(unsafe.Sizeof(droPFiles{}))
	total := headerSize
	encoded := make([][]uint16, 0, len(paths))
	for _, p := range paths {
		u, err := syscall.UTF16FromString(p)
		if err != nil {
			return fmt.Errorf("winui: 剪贴板文件路径转 UTF-16 失败: %w", err)
		}
		encoded = append(encoded, u)
		total += len(u) * 2 // 已包含该路径自身的 NUL 分隔符
	}
	total += 2 // 结尾多出的一个 NUL，即终止列表的空字符串

	buf := make([]byte, total)
	hdr := droPFiles{PFiles: uint32(headerSize), FWide: 1}
	copy(buf, unsafe.Slice((*byte)(unsafe.Pointer(&hdr)), headerSize))
	off := headerSize
	for _, u := range encoded {
		for _, c := range u {
			buf[off] = byte(c)
			buf[off+1] = byte(c >> 8)
			off += 2
		}
	}

	hMem, _, callErr := procGlobalAlloc.Call(GMEM_MOVEABLE|GMEM_ZEROINIT, uintptr(total))
	if hMem == 0 {
		return fmt.Errorf("winui: GlobalAlloc 分配剪贴板内存失败: %w", callErr)
	}
	// 至此所有失败路径都必须释放这块内存；只有 SetClipboardData 成功才把所有权
	// 交给系统。
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("winui: GlobalLock 锁定剪贴板内存失败")
	}
	// 拷贝交由 RtlMoveMemory 完成，而不是在 Go 里把 ptr 转成切片：
	// 把 uintptr 转回指针正是 go vet 的 unsafeptr 所禁止的模式，而这里
	// 的地址只作为参数交给下一次系统调用，从不被 Go 解引用。
	copyToAddress(ptr, buf)
	// GlobalUnlock 必须与 GlobalLock 成对，且在句柄交出去之前完成：留着锁会把
	// 剪贴板卡死，其它进程再也拿不到。
	procGlobalUnlock.Call(hMem)

	if err := openClipboard(); err != nil {
		procGlobalFree.Call(hMem)
		return err
	}
	defer procCloseClipboard.Call()

	r, _, callErr := procEmptyClipboard.Call()
	if r == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("winui: EmptyClipboard 失败: %w", callErr)
	}
	r, _, callErr = procSetClipboardData.Call(CF_HDROP, hMem)
	if r == 0 {
		// 所有权没交出去，所以这里是唯一需要释放的分支。
		procGlobalFree.Call(hMem)
		return fmt.Errorf("winui: SetClipboardData(CF_HDROP) 失败: %w", callErr)
	}
	// 成功：hMem 已归系统所有。此时再 GlobalFree 会二次释放并破坏剪贴板，
	// 正是文档明确禁止的做法。
	return nil
}

// copyToAddress copies src into the raw address dst, which must point at a
// buffer the caller already holds locked (GlobalLock).
//
// RtlMoveMemory is used instead of a Go slice over dst because turning a uintptr
// back into a pointer is the uintptr->pointer conversion go vet's unsafeptr
// check rejects; here the address never becomes a Go pointer, it is only handed
// to the next system call.
func copyToAddress(dst uintptr, src []byte) {
	if len(src) == 0 {
		return
	}
	procRtlMoveMemory.Call(dst, uintptr(unsafe.Pointer(&src[0])), uintptr(len(src)))
	runtimeKeepAlive(src)
}

// openClipboard 有界重试 OpenClipboard。
//
// 剪贴板是单一全局资源，短暂占用会让调用以 ERROR_ACCESS_DENIED 失败；短重试
// 能把这种偶发失败变成正常写入，同时也不会在真有进程长期持有时死等。
func openClipboard() error {
	var lastErr error
	for i := 0; i < clipboardRetries; i++ {
		r, _, err := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		lastErr = err
		if i < clipboardRetries-1 {
			time.Sleep(clipboardDelay)
		}
	}
	return fmt.Errorf("winui: 打开剪贴板失败（可能被其它进程占用，已重试 %d 次）: %w", clipboardRetries, lastErr)
}
