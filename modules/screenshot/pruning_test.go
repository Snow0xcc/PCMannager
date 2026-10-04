package screenshot

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// seedScreenshot writes a fake file that follows the module's real naming
// pattern (screenshot_<unixmilli><ext>) and stamps an explicit mtime so the
// ordering is deterministic regardless of write order.
func seedScreenshot(t *testing.T, dir, name string, mod time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("fake"), 0o600); err != nil {
		t.Fatalf("写入 %s 失败: %v", name, err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatalf("设置 %s 修改时间失败: %v", name, err)
	}
	return p
}

// moduleShots lists the files in dir matching the module's own naming rule.
func moduleShots(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && isModuleScreenshot(e.Name()) {
			names = append(names, e.Name())
		}
	}
	return names
}

// TestInitPrunesOldScreenshots recreates the pre-fix defect scenario: the save
// directory outlives the process, so Init must adopt existing files and delete
// everything past max_history, oldest first.
func TestInitPrunesOldScreenshots(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	var names []string
	for i := 0; i < 10; i++ {
		mod := base.Add(time.Duration(i) * time.Minute)
		name := fmt.Sprintf("screenshot_%d.png", mod.UnixMilli())
		names = append(names, name)
		seedScreenshot(t, dir, name, mod)
	}

	f, ctx := newTestFeature(t, map[string]any{optMaxHistory: 5})
	ctx.DataDir = dir
	if err := f.Init(ctx); err != nil {
		t.Fatalf("Init 不应报错: %v", err)
	}

	remaining := moduleShots(t, dir)
	if len(remaining) != 5 {
		t.Fatalf("清理后剩 %d 张, 期望 5: %v", len(remaining), remaining)
	}
	// The five oldest must be gone, the five newest must survive.
	for _, old := range names[:5] {
		if _, err := os.Stat(filepath.Join(dir, old)); !os.IsNotExist(err) {
			t.Errorf("最旧的文件应被删除: %s", old)
		}
	}
	for _, keep := range names[5:] {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("最新的文件应保留: %s", keep)
		}
	}

	// f.saved must adopt the survivors so the session keeps pruning correctly.
	f.mu.Lock()
	saved := append([]string(nil), f.saved...)
	f.mu.Unlock()
	if len(saved) != 5 {
		t.Fatalf("f.saved 应有 5 项, 实际 %d", len(saved))
	}
}

// TestInitMissingDirIsSilent proves a missing (or empty) save directory never
// turns into an Init failure.
func TestInitMissingDirIsSilent(t *testing.T) {
	f, ctx := newTestFeature(t, nil)
	ctx.DataDir = filepath.Join(ctx.DataDir, "不存在", "screenshot")
	if err := f.Init(ctx); err != nil {
		t.Fatalf("目录缺失时 Init 不应报错: %v", err)
	}
}

// TestInitKeepsForeignFiles proves the cleaner only touches files that match
// the module's own naming rule: screenshot_<digits>.png|.jpg. Anything else —
// user documents, renamed screenshots, directories — must survive.
func TestInitKeepsForeignFiles(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		mod := base.Add(time.Duration(i) * time.Minute)
		seedScreenshot(t, dir, fmt.Sprintf("screenshot_%d.png", mod.UnixMilli()), mod)
	}
	foreign := []string{
		"notes.txt",
		"screenshot_readme.png",      // non-numeric stem: renamed by the user
		"screenshot_backup_2024.jpg", // non-numeric stem
		"IMG_123.png",
		"other_screenshot_123.png",
	}
	for _, name := range foreign {
		seedScreenshot(t, dir, name, base)
	}
	if err := os.Mkdir(filepath.Join(dir, "screenshot_999.png"), 0o755); err != nil {
		t.Fatalf("创建同名目录失败: %v", err)
	}

	f, ctx := newTestFeature(t, map[string]any{optMaxHistory: 100})
	ctx.DataDir = dir
	if err := f.Init(ctx); err != nil {
		t.Fatalf("Init 不应报错: %v", err)
	}

	for _, name := range foreign {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("非本模块产物不应被删除: %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "screenshot_999.png")); err != nil {
		t.Error("目录不应被清理")
	}
	if got := len(moduleShots(t, dir)); got != 3 {
		t.Errorf("本模块截图应保留 3 张, 实际 %d", got)
	}
}

// TestApplyOptionMaxHistoryPrunes runs another cleanup round when the limit
// shrinks at runtime, so a lower max_history takes effect immediately.
func TestApplyOptionMaxHistoryPrunes(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	var names []string
	for i := 0; i < 6; i++ {
		mod := base.Add(time.Duration(i) * time.Minute)
		name := fmt.Sprintf("screenshot_%d.jpg", mod.UnixMilli())
		names = append(names, name)
		seedScreenshot(t, dir, name, mod)
	}

	f, ctx := newTestFeature(t, map[string]any{optMaxHistory: 6})
	ctx.DataDir = dir
	ctx.Bus = core.NewBus()
	if err := f.Init(ctx); err != nil {
		t.Fatalf("Init 不应报错: %v", err)
	}
	if got := len(moduleShots(t, dir)); got != 6 {
		t.Fatalf("上限内不应清理, 实际 %d 张", got)
	}

	// Mirror the app's ordering: the config is persisted first, then the
	// module is notified (internal/app ApplyOption).
	if err := ctx.Config.Set(optMaxHistory, 3); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	if err := f.ApplyOption(optMaxHistory, 3); err != nil {
		t.Fatalf("ApplyOption 失败: %v", err)
	}

	remaining := moduleShots(t, dir)
	if len(remaining) != 3 {
		t.Fatalf("收紧后剩 %d 张, 期望 3: %v", len(remaining), remaining)
	}
	for _, old := range names[:3] {
		if _, err := os.Stat(filepath.Join(dir, old)); !os.IsNotExist(err) {
			t.Errorf("收紧后最旧的应被删除: %s", old)
		}
	}
	for _, keep := range names[3:] {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("收紧后最新的应保留: %s", keep)
		}
	}
}

// TestRecognizesModuleNaming documents the recognition rule used by the
// cleaner: a known prefix (screenshot_ / longshot_) + all-digit stem +
// .png/.jpg. 长截图也是本模块写的文件，必须一并纳入 max_history 的清理，
// 否则长截图跨重启堆积且无人回收。
func TestRecognizesModuleNaming(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"screenshot_1736912345678.png", true},
		{"screenshot_1736912345678.jpg", true},
		{"screenshot_readme.png", false},
		{"screenshot_20240101.png", true}, // any all-digit stem matches the rule
		{"screenshot_.png", false},
		{"screenshot_123.gif", false},
		{"shot_123.png", false},
		{"other_screenshot_123.png", false},
		{"screenshot_123.png.txt", false},
		// 滚动长截图：同前缀规则，必须被识别为本模块文件
		{"longshot_1736912345678.png", true},
		{"longshot_1736912345678.jpg", true},
		{"longshot_readme.png", false},
		{"longshot_.png", false},
		{"longshot_123.gif", false},
		{"xlongshot_123.png", false},
	}
	for _, c := range cases {
		if got := isModuleScreenshot(c.name); got != c.want {
			t.Errorf("isModuleScreenshot(%q) = %v, 期望 %v", c.name, got, c.want)
		}
	}
}
