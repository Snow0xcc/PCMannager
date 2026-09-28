package updater

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestMirrorURL 覆盖各 URL 形态：GitHub 域名包装、非 GitHub 原样、空前缀
// 直连、空串原样、已代理幂等、带路径与 query 的完整 URL。
func TestMirrorURL(t *testing.T) {
	const proxy = "https://ghfast.top/"
	cases := []struct {
		in     string
		prefix string
		want   string
	}{
		// api.github.com / github.com 都要包代理。
		{apiBase, proxy, proxy + apiBase},
		{"https://github.com/Snow0xcc/PCMannager/releases/download/v1.2.3/pcmannager-windows-amd64.exe",
			proxy, proxy + "https://github.com/Snow0xcc/PCMannager/releases/download/v1.2.3/pcmannager-windows-amd64.exe"},
		// 带 query 的完整 URL。
		{"https://api.github.com/repos/x/y?per_page=1", proxy, proxy + "https://api.github.com/repos/x/y?per_page=1"},
		// 空前缀 = 直连兜底，原样返回。
		{apiBase, "", apiBase},
		// 非 GitHub 域名不碰。
		{"https://example.com/foo", proxy, "https://example.com/foo"},
		{"https://evilgithub.com/x", proxy, "https://evilgithub.com/x"},
		{"ftp://github.com/x", proxy, "ftp://github.com/x"},
		// 空串与垃圾输入原样返回（url.Parse 失败或无 host）。
		{"", proxy, ""},
		{"not a url", proxy, "not a url"},
		{"/relative/path", proxy, "/relative/path"},
		// 已带任一镜像前缀的 URL 再包装必须幂等。
		{proxy + apiBase, proxy, proxy + apiBase},
		{"https://ghproxy.net/" + apiBase, proxy, "https://ghproxy.net/" + apiBase},
	}
	for _, c := range cases {
		if got := mirrorURL(c.in, c.prefix); got != c.want {
			t.Errorf("mirrorURL(%q, %q) = %q, 期望 %q", c.in, c.prefix, got, c.want)
		}
	}
}

// TestCandidateURLs 验证候选列表数量等于镜像池大小，且末位为直连 URL。
func TestCandidateURLs(t *testing.T) {
	got := candidateURLs(apiBase)
	if len(got) != len(mirrors) {
		t.Fatalf("candidateURLs 数量 %d != mirrors 数量 %d", len(got), len(mirrors))
	}
	if got[len(got)-1] != apiBase {
		t.Fatalf("末位应为直连 %q, got %q", apiBase, got[len(got)-1])
	}
	for i, m := range mirrors {
		if m == "" {
			continue
		}
		if !strings.HasPrefix(got[i], m) {
			t.Errorf("第 %d 项 %q 应带镜像前缀 %q", i, got[i], m)
		}
	}
}

// TestMirrorHostsAllowed 验证 mirrors 里所有 host 都进了白名单，改镜像池时
// 无需手改 allowedHosts。
func TestMirrorHostsAllowed(t *testing.T) {
	for _, m := range mirrors {
		if m == "" {
			continue
		}
		u, err := url.Parse(m)
		if err != nil || u.Host == "" {
			t.Fatalf("镜像 %q 无法解析出 host", m)
		}
		if !allowedHosts[u.Host] {
			t.Errorf("镜像 host %q 未加入白名单", u.Host)
		}
	}
}

// TestArgsPayloadRoundTrip 验证命令行参数 base64 编码/解码的往返一致性，
// 包括空列表、含空格与中文（PowerShell 转义靠 base64 规避）。分隔符是 \n：
// CreateProcess 命令行为单行，Windows 进程参数不可能含换行，故无冲突场景。
func TestArgsPayloadRoundTrip(t *testing.T) {
	cases := [][]string{
		nil,
		{},
		{"--panel"},
		{"--config", "C:\\Program Files\\GoBox\\config.yaml"},
		{"中文参数", "with space", "--flag=value"},
	}
	for _, args := range cases {
		payload := argsPayload(args)
		got, err := argsFromPayload(payload)
		if err != nil {
			t.Fatalf("argsFromPayload(%q): %v", payload, err)
		}
		if len(got) != len(args) {
			t.Fatalf("往返长度不符: in=%q out=%q", args, got)
		}
		for i := range args {
			if got[i] != args[i] {
				t.Errorf("往返第 %d 项不符: %q != %q", i, got[i], args[i])
			}
		}
		// 载荷不得含引号/换行，保证可安全嵌入 PowerShell 单引号字符串。
		if strings.ContainsAny(payload, "'\"\n") {
			t.Errorf("载荷含不安全字符: %q", payload)
		}
	}
	// 空载荷必须解码为 nil，helper 侧走无参数分支。
	if p := argsPayload(nil); p != "" {
		t.Errorf("空参数载荷应为空串, got %q", p)
	}
	if got, err := argsFromPayload(""); err != nil || got != nil {
		t.Errorf("空载荷应解码为 nil, got %q err=%v", got, err)
	}
}

// TestArgsPayloadIsBase64 抽查编码本身是标准 base64。
func TestArgsPayloadIsBase64(t *testing.T) {
	payload := argsPayload([]string{"abc"})
	dec, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("不是合法 base64: %v", err)
	}
	if string(dec) != "abc" {
		t.Fatalf("解码内容不符: %q", dec)
	}
}

// TestParseUpdateLogEntry 守护“只认当天记录”的语义：当天返回 tag，非当天、
// 坏格式、坏时间戳一律返回空（调用方不得报“更新完成”）。
func TestParseUpdateLogEntry(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 8, 30, 0, 0, time.Local)
	yesterday := today.AddDate(0, 0, -1)

	cases := []struct {
		name string
		data string
		want string
	}{
		{"当天记录", today.Format(time.RFC3339) + "\tv1.2.3\tC:\\app.exe", "v1.2.3"},
		{"非当天不报", yesterday.Format(time.RFC3339) + "\tv1.2.3\tC:\\app.exe", ""},
		{"unknown tag 照样返回", today.Format(time.RFC3339) + "\tunknown\tC:\\app.exe", "unknown"},
		{"缺字段", today.Format(time.RFC3339) + "\tv1.2.3", ""},
		{"坏时间戳", "not-a-time\tv1.2.3\tC:\\app.exe", ""},
		{"空输入", "", ""},
		{"带 CRLF", today.Format(time.RFC3339) + "\tv1.2.3\tC:\\app.exe\r\n", "v1.2.3"},
	}
	for _, c := range cases {
		if got := parseUpdateLogEntry(c.data); got != c.want {
			t.Errorf("%s: parseUpdateLogEntry = %q, 期望 %q", c.name, got, c.want)
		}
	}
}

// TestBuildUpdateScript 验证 helper 脚本包含参数透传、完成日志与重启要素。
func TestBuildUpdateScript(t *testing.T) {
	argsB64 := argsPayload([]string{"--panel"})
	script := buildUpdateScript(`C:\app\pcmannager.exe`, `C:\app\data\pcmannager.update`, argsB64, "v1.2.3")

	for _, want := range []string{
		argsB64,
		"FromBase64String",
		"Start-Process -FilePath $dst -ArgumentList $argList",
		"update.log",
		"v1.2.3",
		"WaitForExit",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("脚本缺少要素 %q", want)
		}
	}
	// 空参数时也要有“无参数也启动”的兜底分支。
	noArgs := buildUpdateScript(`C:\app\pcmannager.exe`, `C:\x`, "", "v9.9.9")
	if !strings.Contains(noArgs, "else { Start-Process -FilePath $dst }") {
		t.Error("脚本缺少无参数重启兜底分支")
	}
}
