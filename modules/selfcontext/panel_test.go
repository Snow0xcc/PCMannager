package selfcontext

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestStateEntriesExposePanelRows（C2-5 数据面）：State()["entries"] 必须给出
// 最近采样的 {title, process, summary, time} 视图，最新在最前；标题/进程/摘要
// 各自截断到 120 字符，时间为 RFC3339。
func TestStateEntriesExposePanelRows(t *testing.T) {
	f, _ := newTestFeature(t, nil)
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	newest := time.Date(2026, 1, 2, 3, 4, 6, 0, time.Local)
	f.mu.Lock()
	f.entries = append(f.entries,
		Entry{Title: "编辑器", Process: "code.exe", Timestamp: old},
		Entry{
			Title:     strings.Repeat("长", 200),
			Process:   strings.Repeat("进", 150),
			Summary:   strings.Repeat("述", 150),
			Timestamp: newest,
		},
	)
	f.last = strings.Repeat("长", 200)
	f.mu.Unlock()

	raw, ok := f.State()["entries"]
	if !ok {
		t.Fatal("State 缺少 entries 键：面板将无法浏览记录")
	}
	rows, ok := raw.([]PanelEntry)
	if !ok {
		t.Fatalf("entries 类型不符：期望 []PanelEntry，实际 %T", raw)
	}
	if len(rows) != 2 {
		t.Fatalf("entries 条数不符：期望 2，实际 %d（%v）", len(rows), rows)
	}

	// 最新在最前；进程字段与标题/摘要同样受 maxPreview 约束：超长进程名
	// 截断为 120 字符 + 省略号，不得整段透传进每次状态广播。
	if rows[0].Process != strings.Repeat("进", 120)+"…" || rows[1].Process != "code.exe" {
		t.Errorf("rows 顺序或进程字段不符：%+v", rows)
	}

	// 标题与摘要截断为 120 字符 + 省略号。
	if got := rows[0].Title; got != strings.Repeat("长", 120)+"…" {
		t.Errorf("长标题应截断为 120 字符+省略号，实际 %d 字符", len([]rune(got)))
	}
	if got := rows[0].Summary; got != strings.Repeat("述", 120)+"…" {
		t.Errorf("长摘要应截断为 120 字符+省略号，实际 %d 字符", len([]rune(got)))
	}
	if got := rows[1].Title; got != "编辑器" {
		t.Errorf("rows[1].Title = %q，期望 编辑器", got)
	}
	if rows[1].Summary != "" {
		t.Errorf("无摘要条目 Summary 应为空串，实际 %q", rows[1].Summary)
	}

	// 时间必须是 RFC3339 且与源时间一致。
	for i, want := range []time.Time{newest, old} {
		if rows[i].Time != want.Format(time.RFC3339) {
			t.Errorf("rows[%d].Time = %q，期望 %q", i, rows[i].Time, want.Format(time.RFC3339))
		}
		if _, err := time.Parse(time.RFC3339, rows[i].Time); err != nil {
			t.Errorf("rows[%d].Time = %q 不是合法 RFC3339：%v", i, rows[i].Time, err)
		}
	}
}

// TestStateEntriesEmptyAndNonNull：未捕获任何记录时 entries 必须是空数组而非
// null——面板据此走空态文案；经 Init（加载持久化）后同样成立，且 JSON 形态
// 必须是 []。
func TestStateEntriesEmptyAndNonNull(t *testing.T) {
	f, ctx := newTestFeature(t, nil)
	if err := f.Init(ctx); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}

	raw, ok := f.State()["entries"]
	if !ok {
		t.Fatal("State 缺少 entries 键")
	}
	rows, ok := raw.([]PanelEntry)
	if !ok {
		t.Fatalf("entries 类型不符：期望 []PanelEntry，实际 %T", raw)
	}
	if rows == nil || len(rows) != 0 {
		t.Fatalf("entries 应为非 nil 空数组，实际 %v", rows)
	}

	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if string(data) != "[]" {
		t.Fatalf("空 entries 的 JSON = %s，期望 []（null 会让面板空态判断失效）", data)
	}
}

// TestStateEntriesBoundedTo30：entries 有界（最近 30 条），500 条的内存缓冲
// 不会让每次状态广播膨胀到 500 行。
func TestStateEntriesBoundedTo30(t *testing.T) {
	f, _ := newTestFeature(t, nil)
	now := time.Now()
	f.mu.Lock()
	for i := 0; i < 35; i++ {
		f.entries = append(f.entries, Entry{Title: "窗口-" + strconv.Itoa(i), Timestamp: now})
	}
	f.mu.Unlock()

	rows, ok := f.State()["entries"].([]PanelEntry)
	if !ok {
		t.Fatal("entries 类型不符")
	}
	if len(rows) != 30 {
		t.Fatalf("entries 应有界为 30 条，实际 %d 条", len(rows))
	}
	if rows[0].Title != "窗口-34" {
		t.Errorf("rows[0] 应是最新条目，实际 %q", rows[0].Title)
	}
	if rows[29].Title != "窗口-5" {
		t.Errorf("rows[29] 应是第 30 新的条目，实际 %q", rows[29].Title)
	}
}

// TestExcerptFlattensControlChars：换行/制表符压平成空格，标题单行展示。
func TestExcerptFlattensControlChars(t *testing.T) {
	got := excerpt("第一行\r\n第二行\n第三行\t末尾", 120)
	if got != "第一行 第二行 第三行 末尾" {
		t.Errorf("excerpt 压平结果 = %q", got)
	}
	if got := excerpt("短", 120); got != "短" {
		t.Errorf("不超限文本不应改动：%q", got)
	}
}
