package screenshot

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// seedShot writes a fake capture file into dir and registers it with
// f.remember, mirroring what writeShotNamed does minus the image encoding.
func seedShot(t *testing.T, f *Feature, dir, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("构造截图文件失败: %v", err)
	}
	f.remember(path)
	return path
}

// TestStateEntriesExposePanelRows（C2-5 数据面）：State()["entries"] 必须给出
// 最近捕获的 {name, kind, size, time} 视图，最新在最前；三类文件前缀各归各的
// 类别，时间取文件名内嵌的毫秒时间戳（RFC3339），大小取真实文件字节数，
// 绝不出现绝对路径。
func TestStateEntriesExposePanelRows(t *testing.T) {
	f, ctx := newTestFeature(t, nil)
	dir := ctx.DataDir

	seedShot(t, f, dir, "screenshot_1700000001000.png", []byte("png-old"))
	seedShot(t, f, dir, "longshot_1700000002000.png", []byte("png-longshot-bytes"))
	seedShot(t, f, dir, "recording_1700000003000.gif", []byte("gif-recording-bytes"))

	raw, ok := f.State()["entries"]
	if !ok {
		t.Fatal("State 缺少 entries 键：面板将无法浏览历史")
	}
	rows, ok := raw.([]PanelShot)
	if !ok {
		t.Fatalf("entries 类型不符：期望 []PanelShot，实际 %T", raw)
	}
	if len(rows) != 3 {
		t.Fatalf("entries 条数不符：期望 3，实际 %d（%v）", len(rows), rows)
	}

	want := []struct {
		name string
		kind string
		size int64
		ms   int64
	}{
		{"recording_1700000003000.gif", "recording", int64(len("gif-recording-bytes")), 1700000003000},
		{"longshot_1700000002000.png", "longshot", int64(len("png-longshot-bytes")), 1700000002000},
		{"screenshot_1700000001000.png", "screenshot", int64(len("png-old")), 1700000001000},
	}
	for i, w := range want {
		got := rows[i]
		if got.Name != w.name {
			t.Errorf("rows[%d].Name = %q，期望 %q（顺序应为最新在前）", i, got.Name, w.name)
		}
		if got.Kind != w.kind {
			t.Errorf("rows[%d].Kind = %q，期望 %q", i, got.Kind, w.kind)
		}
		if got.Size != w.size {
			t.Errorf("rows[%d].Size = %d，期望 %d", i, got.Size, w.size)
		}
		if wantTime := time.UnixMilli(w.ms).Format(time.RFC3339); got.Time != wantTime {
			t.Errorf("rows[%d].Time = %q，期望 %q", i, got.Time, wantTime)
		}
		if filepath.Base(got.Name) != got.Name || filepath.IsAbs(got.Name) {
			t.Errorf("rows[%d].Name 不应是路径：%q", i, got.Name)
		}
	}
}

// TestStateEntriesBoundedTo30：entries 有界（最近 30 条），不随历史增长而
// 膨胀每次状态广播。
func TestStateEntriesBoundedTo30(t *testing.T) {
	f, ctx := newTestFeature(t, nil)
	newest := ""
	for i := 1; i <= 35; i++ {
		ms := strconv.FormatInt(int64(1700000000000+i), 10)
		newest = seedShot(t, f, ctx.DataDir, "screenshot_"+ms+".png", []byte("x"))
	}
	rows, ok := f.State()["entries"].([]PanelShot)
	if !ok {
		t.Fatal("entries 类型不符")
	}
	if len(rows) != 30 {
		t.Fatalf("entries 应有界为 30 条，实际 %d 条", len(rows))
	}
	if got, want := rows[0].Name, filepath.Base(newest); got != want {
		t.Errorf("rows[0] 应是最新文件 %q，实际 %q", want, got)
	}
	if !strings.HasSuffix(rows[29].Name, "1700000000006.png") {
		t.Errorf("rows[29] 应是第 30 新的文件，实际 %q", rows[29].Name)
	}
}

// TestStateEntriesEmptyWithoutHistory：没有任何历史时 entries 必须是空数组
// 而不是 nil/null，面板才能稳定走空态分支。
func TestStateEntriesEmptyWithoutHistory(t *testing.T) {
	f, _ := newTestFeature(t, nil)
	raw, ok := f.State()["entries"]
	if !ok {
		t.Fatal("State 缺少 entries 键")
	}
	rows, ok := raw.([]PanelShot)
	if !ok {
		t.Fatalf("entries 类型不符：期望 []PanelShot，实际 %T", raw)
	}
	if rows == nil || len(rows) != 0 {
		t.Fatalf("entries 应为非 nil 空数组，实际 %v", rows)
	}
}

// TestStateEntriesToleratesMissingFile：文件被用户删除后条目仍在列表里
// （f.saved 不做实时核对），大小退化为 0，时间退回文件名内嵌时间戳，
// State 不得报错。
func TestStateEntriesToleratesMissingFile(t *testing.T) {
	f, ctx := newTestFeature(t, nil)
	path := seedShot(t, f, ctx.DataDir, "screenshot_1700000005000.jpg", []byte("gone"))
	if err := os.Remove(path); err != nil {
		t.Fatalf("清理失败: %v", err)
	}

	rows, ok := f.State()["entries"].([]PanelShot)
	if !ok {
		t.Fatal("entries 类型不符")
	}
	if len(rows) != 1 {
		t.Fatalf("应仍有 1 条，实际 %d", len(rows))
	}
	if rows[0].Size != 0 {
		t.Errorf("文件缺失时 Size 应为 0，实际 %d", rows[0].Size)
	}
	if want := time.UnixMilli(1700000005000).Format(time.RFC3339); rows[0].Time != want {
		t.Errorf("时间应退回文件名时间戳 %q，实际 %q", want, rows[0].Time)
	}
	if rows[0].Name != "screenshot_1700000005000.jpg" {
		t.Errorf("Name = %q，期望保持文件名", rows[0].Name)
	}
	if rows[0].Kind != "screenshot" {
		t.Errorf("Kind = %q，期望 screenshot", rows[0].Kind)
	}
}

// TestShotTimestampRejectsForeignNames：不符合模块命名模式
// （前缀_<正毫秒数><扩展名>）的文件名不给时间戳，面板取时按回退链退回
// 文件 mtime（回退链本身由 TestStateEntriesToleratesMissingFile 覆盖）。
// 时间戳可解析性与类别判定是两条独立轴：时间戳非法 ≠ 陌生文件，
// 类别见 TestShotKindClassifiesByPrefix。
func TestShotTimestampRejectsForeignNames(t *testing.T) {
	for _, name := range []string{
		"report.pdf",             // 无模块前缀
		"screenshot.png",         // 有词干但缺 _<毫秒>
		"screenshot_abc.png",     // 前缀正确但时间戳非数字
		"screenshot_-1.png",      // 数字非法（负数按不可信拒收）
		"longshot_1700000000000", // 缺扩展名
		"recording_.png",         // 毫秒位为空
	} {
		if _, ok := shotTimestamp(name); ok {
			t.Errorf("shotTimestamp(%q) 不应成功", name)
		}
	}
}

// TestShotKindClassifiesByPrefix（C2-5 类别轴）：类别只看文件名前缀，
// 与时间戳是否可解析无关——screenshot_abc.png 仍是 screenshot（其时间
// 退回 mtime），完全陌生的文件名才归 other。
func TestShotKindClassifiesByPrefix(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"report.pdf", "other"},
		{"screenshot.png", "other"},
		{"screenshot_abc.png", "screenshot"},
		{"screenshot_-1.png", "screenshot"},
		{"longshot_1700000000000", "longshot"},
		{"recording_.png", "recording"},
		{"recording_1.mp4", "recording"},
	} {
		if got := shotKind(tc.name); got != tc.want {
			t.Errorf("shotKind(%q) = %q，期望 %q", tc.name, got, tc.want)
		}
	}
}
