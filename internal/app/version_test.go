package app

import "testing"

// TestNormalizedVersion 验证归一化逻辑：CI 用 github.ref_name（tag 名，自带 v
// 前缀）注入 Version，展示层不应再拼出 "vv1.2.3"（ROADMAP A2）。
func TestNormalizedVersion(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()

	cases := []struct {
		stamp, want string
	}{
		{"v1.2.3", "1.2.3"},   // CI 注入的 tag 名，必须去 v
		{"1.2.3", "1.2.3"},    // 已无前缀，保持原样
		{"v0.1.0-rc1", "0.1.0-rc1"},
		{"0.0.0-dev", "0.0.0-dev"},
		{"", ""},              // 空值不得 panic
	}
	for _, c := range cases {
		Version = c.stamp
		if got := NormalizedVersion(); got != c.want {
			t.Errorf("NormalizedVersion() with stamp %q = %q, 期望 %q", c.stamp, got, c.want)
		}
	}
}

// TestPanelProviderVersion 验证面板数据源输出的是归一化版本号。
func TestPanelProviderVersion(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()
	Version = "v9.9.9"

	p := panelProvider{a: &App{}}
	if got := p.Version(); got != "9.9.9" {
		t.Errorf("panelProvider.Version() = %q, 期望 %q（不得重复带 v 前缀）", got, "9.9.9")
	}
}

// TestAppVersionControl 验证 core.AppControl.Version() 输出归一化版本号，
// updater 模块以此为 SemVer 比较基准（ROADMAP A3 的前置）。
func TestAppVersionControl(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()
	Version = "v1.0.0"

	a := &App{}
	if got := a.Version(); got != "1.0.0" {
		t.Errorf("App.Version() = %q, 期望 %q", got, "1.0.0")
	}
}
