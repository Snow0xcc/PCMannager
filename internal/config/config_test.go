package config

import (
	"os"
	"path/filepath"
	"testing"
)

// tempManager loads a config rooted in a throwaway directory.
func tempManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	return m, dir
}

// TestLoadCreatesFileWithDefaults checks a first run materializes config.yaml.
func TestLoadCreatesFileWithDefaults(t *testing.T) {
	m, dir := tempManager(t)

	path := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("应生成配置文件: %v", err)
	}
	if m.Path() != path {
		t.Fatalf("Path() = %q, 期望 %q", m.Path(), path)
	}
	if got := m.Config().App.Language; got != "zh-CN" {
		t.Fatalf("默认语言 = %q, 期望 zh-CN", got)
	}
}

// TestSelfContextDisabledByDefault encodes the privacy requirement (PRD SC-09):
// the screen-recording module must be opt-in.
func TestSelfContextDisabledByDefault(t *testing.T) {
	m, _ := tempManager(t)
	if m.Module("selfcontext").Enabled() {
		t.Fatal("selfcontext 默认必须关闭（隐私）")
	}
	if !m.Module("taskbar").Enabled() {
		t.Fatal("taskbar 默认应启用")
	}
}

// TestModuleViewSetPersistsAndSurvivesReload verifies Set writes through to
// disk so a fresh Load sees the same value.
func TestModuleViewSetPersistsAndSurvivesReload(t *testing.T) {
	m, dir := tempManager(t)
	if err := m.Module("clipboard").Set("max_items", 42); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	if got := reloaded.Module("clipboard").Get("max_items", 0); got != 42 {
		t.Fatalf("重载后 max_items = %v, 期望 42", got)
	}
}

// TestSetEnabledAndHotkeyPersist covers the two switches the panel drives.
func TestSetEnabledAndHotkeyPersist(t *testing.T) {
	m, dir := tempManager(t)
	if err := m.Module("screenshot").SetEnabled(false); err != nil {
		t.Fatalf("SetEnabled 失败: %v", err)
	}
	if err := m.Module("screenshot").SetHotkey("ctrl+alt+z"); err != nil {
		t.Fatalf("SetHotkey 失败: %v", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	if reloaded.Module("screenshot").Enabled() {
		t.Fatal("重载后 screenshot 仍为启用")
	}
	if got := reloaded.Module("screenshot").Hotkey(); got != "ctrl+alt+z" {
		t.Fatalf("重载后热键 = %q, 期望 ctrl+alt+z", got)
	}
}

// TestGetFallsBackToDeclaredDefaults covers the three-level resolution:
// persisted value, declared default, then caller fallback.
func TestGetFallsBackToDeclaredDefaults(t *testing.T) {
	m, _ := tempManager(t)
	m.DeclareDefaults("custom", map[string]any{"alpha": "declared"})

	if got := m.Module("custom").Get("alpha", "fallback"); got != "declared" {
		t.Fatalf("alpha = %v, 期望 declared", got)
	}
	if got := m.Module("custom").Get("missing", "fallback"); got != "fallback" {
		t.Fatalf("missing = %v, 期望 fallback", got)
	}
	// A persisted value wins over the declared default.
	if err := m.Module("custom").Set("alpha", "stored"); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	if got := m.Module("custom").Get("alpha", "fallback"); got != "stored" {
		t.Fatalf("alpha = %v, 期望 stored", got)
	}
}

// TestLoadBackfillsNewModules ensures a config written by an older build gains
// modules introduced later, while keeping the user's own values.
func TestLoadBackfillsNewModules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// Simulate an old config that only knows about taskbar.
	old := "app:\n  theme: auto\nmodules:\n  taskbar:\n    enabled: false\n    hotkey: ctrl+alt+9\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatalf("写入旧配置失败: %v", err)
	}

	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	// The user's value must survive the backfill.
	if m.Module("taskbar").Enabled() {
		t.Fatal("旧配置中的 enabled:false 被覆盖")
	}
	if got := m.Module("taskbar").Hotkey(); got != "ctrl+alt+9" {
		t.Fatalf("旧热键被覆盖: %q", got)
	}
	// A module the old file never mentioned is still present.
	if _, ok := m.Config().Modules["clipboard"]; !ok {
		t.Fatal("新版模块未被回填")
	}
}

// TestSaveIsAtomic guards against a temporary file being left behind and
// against the config becoming unreadable after a save.
func TestSaveIsAtomic(t *testing.T) {
	m, dir := tempManager(t)
	if err := m.Module("taskbar").SetEnabled(false); err != nil {
		t.Fatalf("SetEnabled 失败: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("原子写残留临时文件: %s", e.Name())
		}
	}

	// The saved document must still parse.
	if _, err := Load(dir); err != nil {
		t.Fatalf("保存后配置不可解析: %v", err)
	}
}

// TestUpdateAppPersists checks the app section round-trips.
func TestUpdateAppPersists(t *testing.T) {
	m, dir := tempManager(t)
	if err := m.UpdateApp(func(c *App) {
		c.Autostart = true
		c.ServerPort = 8787
	}); err != nil {
		t.Fatalf("UpdateApp 失败: %v", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	app := reloaded.App()
	if !app.Autostart {
		t.Fatal("autostart 未持久化")
	}
	if app.ServerPort != 8787 {
		t.Fatalf("server_port = %d, 期望 8787", app.ServerPort)
	}
}

// TestModuleIDsIsSorted makes panel ordering deterministic.
func TestModuleIDsIsSorted(t *testing.T) {
	m, _ := tempManager(t)
	ids := m.ModuleIDs()
	for i := 1; i < len(ids); i++ {
		if ids[i-1] > ids[i] {
			t.Fatalf("模块 ID 未排序: %v", ids)
		}
	}
	if len(ids) == 0 {
		t.Fatal("应有默认模块")
	}
}
