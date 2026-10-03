//go:build darwin

package sysutil

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestDarwinAutostartPlistEntryPointsAtExecutable 守护 macOS 自启条目内容：条目里
// 必须同时包含 Label、当前可执行文件绝对路径和 RunAtLoad，否则登录时不会拉起进程。
//
// 之所以单独成文件：autostartPlist 只在 darwin 构建中存在，共享的
// sysutil_autostart_test.go（!windows）不能直接引用它，否则 Linux 上构建失败。
func TestDarwinAutostartPlistEntryPointsAtExecutable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const name = "GoBox"
	if err := SetAutostart(name, true); err != nil {
		t.Fatalf("SetAutostart(true): %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	path, label := autostartPlist(name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 plist: %v", err)
	}
	content := string(data)
	for _, want := range []string{label, exe, "RunAtLoad"} {
		if !strings.Contains(content, want) {
			t.Errorf("plist 缺少 %q\n内容:\n%s", want, content)
		}
	}
}

// TestPlistIsWellFormed 用系统的 plutil 做权威校验：plist 是 launchd 的输入，
// 格式错误会导致自启静默失效（launchd 不弹错），因此这里必须是真校验而不是
// 字符串拼装检查。
func TestPlistIsWellFormed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const name = "GoBox"
	if err := SetAutostart(name, true); err != nil {
		t.Fatalf("SetAutostart: %v", err)
	}
	path, _ := autostartPlist(name)
	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v\n%s", err, out)
	}
}
