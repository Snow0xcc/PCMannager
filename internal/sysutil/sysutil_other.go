//go:build !windows && !darwin

package sysutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Linux（及其它非 Windows、非 macOS 的 Unix）平台实现。
// macOS 的对应实现在 sysutil_darwin.go；单实例/提权等共用部分在 sysutil_unix.go。

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

// ShowInFolder reveals a file in the platform file manager.
func ShowInFolder(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if !fileExists("/usr/bin/xdg-open") {
		return fmt.Errorf("未找到文件管理器")
	}
	return exec.Command("xdg-open", filepath.Dir(abs)).Start()
}

// OpenTerminalHere opens a terminal at dir.
func OpenTerminalHere(dir string) error {
	if !fileExists("/usr/bin/x-terminal-emulator") {
		return fmt.Errorf("未找到终端")
	}
	return exec.Command("x-terminal-emulator", "--working-directory="+dir).Start()
}

// OpenURL opens a URL with the platform handler.
func OpenURL(target string) error {
	if target == "" {
		return fmt.Errorf("open: 目标为空")
	}
	if !fileExists("/usr/bin/xdg-open") {
		return fmt.Errorf("未找到 xdg-open")
	}
	return exec.Command("xdg-open", target).Start()
}
