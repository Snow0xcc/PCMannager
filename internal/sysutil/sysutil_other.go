//go:build !windows

package sysutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// hideWindow is a no-op off Windows.
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

// SetAutostart writes/removes a desktop autostart entry.
//
// On Linux this uses ~/.config/autostart (XDG), which keeps the feature
// functional for development even though Windows is the primary target.
func SetAutostart(name string, on bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".config", "autostart")
	entry := filepath.Join(dir, name+".desktop")

	if !on {
		if err := os.Remove(entry); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content := "[Desktop Entry]\nType=Application\nName=" + name +
		"\nExec=" + exe + "\nX-GNOME-Autostart-enabled=true\n"
	return os.WriteFile(entry, []byte(content), 0o644)
}

// IsAutostart reports whether the desktop autostart entry exists.
func IsAutostart(name string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".config", "autostart", name+".desktop"))
	return err == nil
}

// AcquireSingleInstance takes an exclusive advisory lock on a lock file.
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

// Notify is a no-op off Windows.
func Notify(title, message string) error {
	return fmt.Errorf("通知仅支持 Windows")
}

// OpenURL opens a URL with the platform handler.
func OpenURL(target string) error {
	var cmd string
	var args []string
	switch {
	case fileExists("/usr/bin/xdg-open"):
		cmd, args = "xdg-open", []string{target}
	default:
		return fmt.Errorf("未找到 xdg-open")
	}
	return exec.Command(cmd, args...).Start()
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
