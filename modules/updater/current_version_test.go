package updater

import (
	"testing"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// stubAppControl 提供 Version() 的假实现，模拟 ldflags stamp 过的发布构建。
type stubAppControl struct{ version string }

func (s stubAppControl) Version() string                     { return s.version }
func (s stubAppControl) Shutdown()                           {}
func (s stubAppControl) PanelURL() string                    { return "" }
func (s stubAppControl) OpenPanel() error                    { return nil }
func (s stubAppControl) EnableModule(string, bool) error     { return nil }

// TestCurrentVersionUsesAppControl 验证 currentVersion 以 ctx.App.Version()
// 为基准（A3）：发布构建的 ldflags stamp 只有 internal/app 知道，
// debug.ReadBuildInfo() 只会返回 "(devel)" → 0.0.0-dev，导致当前 tag
// 被自己判成新版、永久误报。这个 seam（core.AppControl.Version）就是为此而设。
func TestCurrentVersionUsesAppControl(t *testing.T) {
	f := &Feature{}
	f.ctx = &core.Context{
		Config: &stubConfig{vals: map[string]any{}},
		App:    stubAppControl{version: "1.2.3"},
	}
	if got := f.currentVersion(); got != "1.2.3" {
		t.Fatalf("currentVersion() = %q, 期望 1.2.3（应来自 AppControl.Version）", got)
	}
}

// TestCurrentVersionFallbackWithoutApp 兜底：上下文没有 App 时回退
// 0.0.0-dev（与旧行为一致），不得 panic。
func TestCurrentVersionFallbackWithoutApp(t *testing.T) {
	f := &Feature{}
	f.ctx = &core.Context{Config: &stubConfig{vals: map[string]any{}}}
	if got := f.currentVersion(); got != "0.0.0-dev" {
		t.Fatalf("currentVersion() = %q, 期望 0.0.0-dev", got)
	}
}

// TestIsNewerAgainstStampedSelfVersion 回归用例：v1.2.3 构建对 v1.2.3 tag
// 不得报"有新版"——A3 缺陷正是当前版本被误判为 0.0.0-dev 后把同版本判新。
func TestIsNewerAgainstStampedSelfVersion(t *testing.T) {
	f := &Feature{}
	f.ctx = &core.Context{
		Config: &stubConfig{vals: map[string]any{"include_prerelease": false}},
		App:    stubAppControl{version: "1.2.3"},
	}
	f.last.Current = f.currentVersion()
	if f.isNewer("v1.2.3") {
		t.Fatal("同版本（stamp 1.2.3 vs tag v1.2.3）被误判为有新版")
	}
	if !f.isNewer("v1.2.4") {
		t.Fatal("stamp 1.2.3 应识别 v1.2.4 为更新")
	}
}
