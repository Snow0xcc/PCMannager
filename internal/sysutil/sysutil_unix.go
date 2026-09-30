//go:build unix

package sysutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// Unix 平台（Linux 与 macOS）共享的实现：单实例锁、提权语义与辅助函数。
// 平台差异部分（自启动、文件管理器、终端、URL 打开器）分别在
// sysutil_linux.go（经 sysutil_other.go 的 !windows && !darwin 标签）与
// sysutil_darwin.go 中。

// hideWindow is a no-op on Unix: there is no window-station concept to hide.
func hideWindow(*exec.Cmd) {}

// IsElevated reports whether the process runs as root.
func IsElevated() bool { return os.Geteuid() == 0 }

// Elevate is unsupported off Windows: GoBox's Windows-first modules are the
// only callers, and silently re-running under sudo would be surprising.
func Elevate([]string) error {
	return fmt.Errorf("提权仅支持 Windows")
}

// RunElevated is unsupported off Windows.
func RunElevated(string) error { return fmt.Errorf("提权执行仅支持 Windows") }

// RunElevatedPath is unsupported off Windows.
func RunElevatedPath(path string, args []string) error {
	return fmt.Errorf("以管理员身份运行仅支持 Windows")
}

// AcquireSingleInstance takes an exclusive advisory lock on a lock file.
//
// flock 在 Linux 与 macOS 上语义一致（BSD 锁），一份实现两端共用。
func AcquireSingleInstance(name string) (release func(), ok bool) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}, false
	}
	// LOCK_EX|LOCK_NB fails immediately when another process holds the lock.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return func() {}, false
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
