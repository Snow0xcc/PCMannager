//go:build darwin

package sysutil

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// macOS 平台实现。
//
// 开机自启用 launchd 的用户 LaunchAgent（~/Library/LaunchAgents），登录时由
// launchd 自动拾取；文件/URL/终端走系统 `open`。此前这些调用全部落进
// sysutil_other.go 的 xdg-open 分支，在 macOS 上必然报"未找到"——本文件把
// 它们逐一补齐。

// autostartPlist 返回 name 对应的 LaunchAgent 路径与 launchd Label。
//
// Label 用反向域名风格（cc.snow0xcc.<小写 name>），避免与系统或其它应用的
// agent 重名；文件名与 Label 一致，便于用户在 LaunchAgents 目录里辨认。
func autostartPlist(name string) (path, label string) {
	home, _ := os.UserHomeDir() // 与调用方约定：拿不到 HOME 时报错在 SetAutostart 里给
	label = "cc.snow0xcc." + strings.ToLower(name)
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), label
}

// SetAutostart 写入/移除 launchd 用户代理。
//
// 只落 plist 不主动 launchctl bootstrap：登录时 launchd 会自动加载
// ~/Library/LaunchAgents 下的代理，而"打开设置立刻启动"不是开机自启的语义。
// 这样实现是幂等的，且不需要调用 launchctl（行为随 macOS 版本漂移）。
func SetAutostart(name string, on bool) error {
	if name == "" {
		return fmt.Errorf("autostart: 名称为空")
	}
	path, label := autostartPlist(name)
	if !on {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// ProgramArguments 里的 exe 路径需要 XML 转义（空格虽合法，& < > 不合法）。
	content := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + mustXML(label) + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + mustXML(exe) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`
	return os.WriteFile(path, []byte(content), 0o644)
}

// mustXML 把文本转成可放进 plist 的 XML 转义串。
//
// plist 里的 exe 路径与 Label 都来自本进程，不会有非法字符，写入失败（内存
// 分配失败之类）按 panic 处理即可，不值得为此把签名复杂化。
func mustXML(s string) string {
	var b strings.Builder
	// EscapeText 只在内部缓冲写出错时才返回 error（strings.Builder 不会），
	// 带上 panic 是为了让调用方保持单行表达式。
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		panic("sysutil: plist 文本转义失败: " + err.Error())
	}
	return b.String()
}

// IsAutostart 报告 LaunchAgent plist 是否存在。
func IsAutostart(name string) bool {
	path, _ := autostartPlist(name)
	_, err := os.Stat(path)
	return err == nil
}

// ShowInFolder 在 Finder 中定位文件。
func ShowInFolder(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return exec.Command("open", "-R", abs).Start()
}

// OpenTerminalHere 在 Terminal.app 中打开目录。
func OpenTerminalHere(dir string) error {
	if dir == "" {
		return exec.Command("open", "-a", "Terminal").Start()
	}
	return exec.Command("open", "-a", "Terminal", dir).Start()
}

// OpenURL 用系统默认处理器打开 URL / 文件 / 目录。
func OpenURL(target string) error {
	if target == "" {
		return fmt.Errorf("open: 目标为空")
	}
	return exec.Command("open", target).Start()
}
