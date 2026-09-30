//go:build !windows

package sysutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAutostartRoundTrip 守护开机自启的完整往返：写入 → IsAutostart 为真 →
// 移除 → 为假。同一份测试在 Linux 上走 XDG、macOS 上走 launchd（构建标签决定），
// 两端语义必须一致：on 幂等、off 幂等、移除不存在的条目不算错误。
func TestAutostartRoundTrip(t *testing.T) {
	// os.UserHomeDir 读 $HOME，把它隔离进临时目录，测试绝不会碰真实配置。
	home := t.TempDir()
	t.Setenv("HOME", home)

	const name = "GoBox"
	if IsAutostart(name) {
		t.Fatal("前置条件：初始状态不应有自启条目")
	}

	if err := SetAutostart(name, true); err != nil {
		t.Fatalf("SetAutostart(true): %v", err)
	}
	if !IsAutostart(name) {
		t.Fatal("开启后 IsAutostart 应为真")
	}
	// 幂等：重复开启不报错。
	if err := SetAutostart(name, true); err != nil {
		t.Fatalf("重复开启应幂等: %v", err)
	}

	if err := SetAutostart(name, false); err != nil {
		t.Fatalf("SetAutostart(false): %v", err)
	}
	if IsAutostart(name) {
		t.Fatal("关闭后 IsAutostart 应为假")
	}
	// 幂等：移除不存在的条目不算错误。
	if err := SetAutostart(name, false); err != nil {
		t.Fatalf("重复移除应幂等: %v", err)
	}
}

// TestAutostartEntryPointsAtExecutable 守护条目内容：自启的目的是"登录时把
// 这个二进制跑起来"，所以条目里必须指向当前可执行文件的绝对路径。
func TestAutostartEntryPointsAtExecutable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const name = "GoBox"
	if err := SetAutostart(name, true); err != nil {
		t.Fatalf("SetAutostart(true): %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	var entry string
	switch runtime.GOOS {
	case "darwin":
		path, label := autostartPlist(name)
		entry = path
		data, err := os.ReadFile(entry)
		if err != nil {
			t.Fatalf("读 plist: %v", err)
		}
		content := string(data)
		for _, want := range []string{label, exe, "RunAtLoad"} {
			if !strings.Contains(content, want) {
				t.Errorf("plist 缺少 %q\n内容:\n%s", want, content)
			}
		}
	default:
		entry = filepath.Join(home, ".config", "autostart", name+".desktop")
		data, err := os.ReadFile(entry)
		if err != nil {
			t.Fatalf("读 desktop 条目: %v", err)
		}
		if !strings.Contains(string(data), exe) {
			t.Errorf("desktop 条目缺少可执行路径 %q\n内容:\n%s", exe, data)
		}
	}
	if entry == "" {
		t.Fatal("条目路径为空")
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("条目文件不存在: %v", err)
	}
}

// TestPlistIsWellFormed 只在 macOS 上跑：plist 是 launchd 的输入，格式错误
// 会导致自启静默失效（launchd 不弹错）。用系统的 plutil 做权威校验。
func TestPlistIsWellFormed(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("仅 macOS 有 plutil")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	const name = "GoBox"
	if err := SetAutostart(name, true); err != nil {
		t.Fatalf("SetAutostart: %v", err)
	}
	path, _ := autostartPlist(name)
	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v\n%s", err, out)
	}
}

// TestAutostartEmptyNameRejected 守护空名称被拒绝：Label/文件名来自 name，
// 空字符串会生成无意义的 ".plist"。
func TestAutostartEmptyNameRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := SetAutostart("", true); err == nil {
		t.Fatal("空名称应报错")
	}
}
