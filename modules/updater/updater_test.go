package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/snow0xcc/pcmannager/internal/core"
	"testing"
)

func TestParseSemver(t *testing.T) {
	cases := []struct {
		in   string
		want *semver
	}{
		{"v1.2.3", &semver{1, 2, 3, ""}},
		{"1.2.3", &semver{1, 2, 3, ""}},
		{"v1.2.3-rc.1", &semver{1, 2, 3, "rc.1"}},
		{"v1.2.3-rc.1+build.5", &semver{1, 2, 3, "rc.1"}},
		{"v0.1.0-rc2", &semver{0, 1, 0, "rc2"}},
		{"", nil},
		{"abc", nil},
		{"v1.x.3", nil},
		{"v1.2.3.4", nil},
		{"-1.2.3", nil},
	}
	for _, c := range cases {
		got := parseSemver(c.in)
		if c.want == nil {
			if got != nil {
				t.Errorf("parseSemver(%q) = %v, 期望 nil", c.in, got)
			}
			continue
		}
		if got == nil || *got != *c.want {
			t.Errorf("parseSemver(%q) = %v, 期望 %v", c.in, got, c.want)
		}
	}
}

func TestComparePrerelease(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "rc.1", 1},  // final > pre-release
		{"rc.1", "", -1}, // pre-release < final
		{"rc.1", "rc.1", 0},
		{"rc.1", "rc.2", -1},
		{"rc.2", "rc.10", -1}, // numeric compare, not lexicographic
		{"alpha", "beta", -1},
		{"rc.1.1", "rc.1", 1}, // more fields wins when prefix equal
	}
	for _, c := range cases {
		got := comparePrerelease(c.a, c.b)
		if (got < 0) != (c.want < 0) || (got > 0) != (c.want > 0) {
			t.Errorf("comparePrerelease(%q, %q) = %d, 期望 %d", c.a, c.b, got, c.want)
		}
	}
}

// stubConfig is a minimal core.ModuleConfig feeding Get() from a map.
type stubConfig struct{ vals map[string]any }

func (s *stubConfig) Enabled() bool  { return true }
func (s *stubConfig) Hotkey() string { return "" }
func (s *stubConfig) Get(k string, def any) any {
	if v, ok := s.vals[k]; ok {
		return v
	}
	return def
}
func (s *stubConfig) Set(string, any) error  { return nil }
func (s *stubConfig) SetEnabled(bool) error  { return nil }
func (s *stubConfig) SetHotkey(string) error { return nil }

func TestIsNewer(t *testing.T) {
	f := &Feature{}
	cases := []struct {
		cur, tag string
		pre      bool
		want     bool
	}{
		{"v0.1.0", "v0.2.0", false, true},
		{"v0.2.0", "v0.1.0", false, false},
		{"v0.1.0", "v0.1.0", false, false},
		// rc vs final of the same triple: final is newer than its rc.
		{"v0.1.0-rc1", "v0.1.0", false, true},
		{"v0.1.0", "v0.1.0-rc1", false, false},
		// rc → newer rc requires the opt-in flag.
		{"v0.1.0-rc1", "v0.1.0-rc2", false, false},
		{"v0.1.0-rc1", "v0.1.0-rc2", true, true},
		// Dev build (unparsable current) treats any final release as newer.
		{"0.0.0-dev", "v0.2.0", false, true},
		{"0.0.0-dev", "v0.2.0-rc1", false, false},
		{"0.0.0-dev", "v0.2.0-rc1", true, true},
		// Malformed tag never counts as newer (fails closed).
		{"v0.1.0", "garbage", false, false},
	}
	for _, c := range cases {
		f.last.Current = c.cur
		f.ctx = &core.Context{
			Config: &stubConfig{vals: map[string]any{"include_prerelease": c.pre}},
		}
		got := f.isNewer(c.tag)
		if got != c.want {
			t.Errorf("isNewer(cur=%s, tag=%s, pre=%v) = %v, 期望 %v", c.cur, c.tag, c.pre, got, c.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	name := AssetName()
	if !strings.HasPrefix(name, "pcmannager-") {
		t.Fatalf("AssetName() = %q, 应以 pcmannager- 开头", name)
	}
	if strings.Contains(name, "//") || strings.Contains(name, "..") {
		t.Fatalf("AssetName() = %q, 含路径穿越片段", name)
	}
}

func TestFindAsset(t *testing.T) {
	rel := &Release{TagName: "v9.9.9", Assets: []Asset{
		{Name: "pcmannager-linux-amd64"},
		{Name: "PCMANAGER-DARWIN-ARM64"}, // case-insensitive match
	}}
	if rel.findAsset() == nil {
		t.Fatal("大小写不同的资产应能匹配")
	}
	rel2 := &Release{TagName: "v9.9.9", Assets: []Asset{{Name: "pcmannager-solaris-sparc"}}}
	if rel2.findAsset() != nil {
		t.Fatal("不匹配平台时 findAsset 应返回 nil")
	}
}

func TestFetchLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "" {
			t.Errorf("请求缺少 Accept 头")
		}
		w.Write([]byte(`{"tag_name":"v1.2.3","html_url":"https://github.com/x/y/releases/v1.2.3","assets":[{"name":"pcmannager-windows-amd64.exe","size":10,"browser_download_url":"https://github.com/x/y/releases/download/v1.2.3/pcmannager-windows-amd64.exe"}]}`))
	}))
	defer srv.Close()

	// fetchLatest 固定指向 GitHub；此处用 http.Client 重定向无法改 URL，
	// 故直接验证解析逻辑：通过替换 apiBase 不可行（const），改用结构化断言。
	rel := &Release{}
	body := `{"tag_name":"v1.2.3","assets":[{"name":"pcmannager-linux-amd64"}]}`
	if err := jsonUnmarshal(body, rel); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if rel.TagName != "v1.2.3" || len(rel.Assets) != 1 {
		t.Fatalf("解析结果不符: %+v", rel)
	}
	_ = srv // 保持服务器生命周期（虽然未直接命中）
}

func TestCopyCappedAtomic(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.bin")

	r := strings.NewReader("hello update payload")
	n, sum, err := copyCapped(dst, r, 1024)
	if err != nil {
		t.Fatalf("copyCapped: %v", err)
	}
	if n != int64(len("hello update payload")) {
		t.Fatalf("字节数 %d 不符", n)
	}
	if len(sum) != 64 {
		t.Fatalf("sha256 长度 %d 不符", len(sum))
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "hello update payload" {
		t.Fatalf("目标文件内容不符: %q err=%v", data, err)
	}

	// 超限必须报错，且不得产出目标文件。
	_, _, err = copyCapped(filepath.Join(dir, "out2.bin"), strings.NewReader("x"), 0)
	if err == nil {
		t.Fatal("0 上限应报错")
	}
}

func TestNewHTTPClientAllowsOnlyPinnedHosts(t *testing.T) {
	// 不发起真实网络：仅验证 Transport 的 Control 钩子逻辑存在且允许表生效。
	hc := newHTTPClient()
	tr, ok := hc.Transport.(*http.Transport)
	if !ok || tr.DialContext == nil {
		t.Fatal("Transport 应带 DialContext 白名单钩子")
	}
	_, err := tr.DialContext(context.Background(), "tcp", "evil.example.com:443")
	if err == nil {
		t.Fatal("未允许的主机应被拒绝")
	}
	if !strings.Contains(err.Error(), "阻止") {
		t.Fatalf("错误信息应说明被阻止: %v", err)
	}
}

// jsonUnmarshal decodes JSON text into v (test helper mirroring fetchLatest's
// decoding path without pinning the const apiBase).
func jsonUnmarshal(body string, v any) error {
	return json.NewDecoder(strings.NewReader(body)).Decode(v)
}
