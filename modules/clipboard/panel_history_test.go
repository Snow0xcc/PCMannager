package clipboard

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// waitRemoved 轮询等待路径消失：onDelete 回调在独立 goroutine 中执行，
// 删除与文件清理之间没有先后保证。
func waitRemoved(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待缓存文件被清理超时：%s 仍存在", path)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestStateEntriesExposePanelRows（C2-1 数据面）：State()["entries"] 必须给出
// 最近条目的 {id, kind, preview, time} 视图，最新在最前，三种 kind 的预览
// 规则分别是：文本取前 120 字符、图片用已存大小、文件用名称。
func TestStateEntriesExposePanelRows(t *testing.T) {
	f, _ := newIngestFeature(t, map[string]any{"store_images": true})

	textEntry := f.hist.AddText("第一行\n第二行")
	imgEntry := f.hist.AddImageFile(filepath.Join("\\\\cache", "img_1.png"), 2048)
	fileEntry := f.hist.AddFileEntry(filepath.Join("C:\\Users", "report.pdf"), "report.pdf")

	st := f.State()
	raw, ok := st["entries"]
	if !ok {
		t.Fatal("State 缺少 entries 键：面板将无法浏览历史")
	}
	rows, ok := raw.([]PanelEntry)
	if !ok {
		t.Fatalf("entries 类型不符：期望 []PanelEntry，实际 %T", raw)
	}
	if len(rows) != 3 {
		t.Fatalf("entries 条数不符：期望 3，实际 %d（%v）", len(rows), rows)
	}

	// 最新在最前：file → image → text。
	want := []struct {
		id      int
		kind    string
		preview string
	}{
		{fileEntry.ID, "file", "[文件] report.pdf"},
		{imgEntry.ID, "image", "[图片 2.0 KB]"},
		{textEntry.ID, "text", "第一行 第二行"},
	}
	for i, w := range want {
		got := rows[i]
		if got.ID != w.id {
			t.Errorf("rows[%d].ID = %d，期望 %d（顺序应为最新在前）", i, got.ID, w.id)
		}
		if got.Kind != w.kind {
			t.Errorf("rows[%d].Kind = %q，期望 %q", i, got.Kind, w.kind)
		}
		if got.Preview != w.preview {
			t.Errorf("rows[%d].Preview = %q，期望 %q", i, got.Preview, w.preview)
		}
		if got.Time == "" {
			t.Errorf("rows[%d].Time 为空：面板需要可显示的时间", i)
		} else if _, err := time.Parse(time.RFC3339, got.Time); err != nil {
			t.Errorf("rows[%d].Time = %q 不是 RFC3339：%v", i, got.Time, err)
		}
	}

	// 与 entries 同源：last 的预览应与最后一条（最旧）一致。
	if last, _ := st["last"].(string); last != "[文件] report.pdf" {
		t.Errorf("State[last] = %q，期望与最旧条目一致", last)
	}
}

// TestStateEntriesBoundedTo30：entries 有界（最近 30 条），不随历史增长而
// 膨胀每次状态广播。
func TestStateEntriesBoundedTo30(t *testing.T) {
	f, _ := newIngestFeature(t, nil)
	var newest Entry
	for i := 1; i <= 35; i++ {
		newest = f.hist.AddText(fmt.Sprintf("条目-%02d", i))
	}
	rows, ok := f.State()["entries"].([]PanelEntry)
	if !ok {
		t.Fatal("entries 类型不符")
	}
	if len(rows) != 30 {
		t.Fatalf("entries 应有界为 30 条，实际 %d 条", len(rows))
	}
	if rows[0].ID != newest.ID {
		t.Errorf("rows[0] 应是最新条目 ID=%d，实际 %d", newest.ID, rows[0].ID)
	}
	if rows[29].ID != newest.ID-29 {
		t.Errorf("rows[29] 应是 ID=%d，实际 %d", newest.ID-29, rows[29].ID)
	}
}

// TestStateEntriesPreviewCapped：文本预览截断到 120 字符（含省略号），
// 控制字符压平成空格。
func TestStateEntriesPreviewCapped(t *testing.T) {
	f, _ := newIngestFeature(t, nil)

	long := strings.Repeat("字", 200)
	f.hist.AddText(long)
	rows, _ := f.State()["entries"].([]PanelEntry)
	want := strings.Repeat("字", 120) + "…"
	if rows[0].Preview != want {
		t.Errorf("长文本预览应截断为 120 字符+省略号（共 %d 字符），实际 %d 字符",
			len([]rune(want)), len([]rune(rows[0].Preview)))
	}

	f.hist.AddText("a\nb\r\nc\td")
	rows, _ = f.State()["entries"].([]PanelEntry)
	if rows[0].Preview != "a b c d" {
		t.Errorf("多行文本应压平为单行：实际 %q", rows[0].Preview)
	}
}

// TestRunActionDeleteEntry（C2-1 动作面）：delete_entry 按 ID 真删条目并联动
// 清理图片缓存文件；id 缺失/非法/不存在都必须得到中文错误。
func TestRunActionDeleteEntry(t *testing.T) {
	f, _ := newIngestFeature(t, map[string]any{"store_images": true})

	pngPath := filepath.Join(f.ctx.DataDir, "img_del.png")
	if err := os.WriteFile(pngPath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("构造缓存文件失败: %v", err)
	}
	img := f.hist.AddImageFile(pngPath, 9)
	f.hist.SetOnDelete(func(e Entry) {
		if e.Kind == KindImage && e.Path != "" {
			os.Remove(e.Path)
		}
	})

	t.Run("合法 ID 真删并清理缓存文件", func(t *testing.T) {
		if err := f.RunAction("delete_entry", map[string]string{"id": strconv.Itoa(img.ID)}); err != nil {
			t.Fatalf("delete_entry 失败: %v", err)
		}
		if _, ok := f.hist.Get(img.ID); ok {
			t.Error("条目未被删除")
		}
		waitRemoved(t, pngPath)
	})

	t.Run("参数错误给出中文错误", func(t *testing.T) {
		cases := []struct {
			name   string
			params map[string]string
			want   string
		}{
			{name: "缺少 id", params: nil, want: "缺少"},
			{name: "空 id", params: map[string]string{"id": ""}, want: "缺少"},
			{name: "非数字 id", params: map[string]string{"id": "abc"}, want: "无效"},
			{name: "负数 id", params: map[string]string{"id": "-3"}, want: "无效"},
			{name: "不存在的 id", params: map[string]string{"id": "424242"}, want: "不存在"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := f.RunAction("delete_entry", tc.params)
				if err == nil {
					t.Fatal("期望报错，实际成功")
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("错误信息 %q 未包含 %q", err.Error(), tc.want)
				}
			})
		}
		if got := len(f.hist.All()); got != 0 {
			t.Errorf("失败调用不应改动历史：实际剩 %d 条", got)
		}
	})
}

// TestRunActionWriteEntry：write_entry 按 ID 分派写回。查错路径全平台确定；
// 写回路径依赖真实剪贴板，按平台语义断言。
func TestRunActionWriteEntry(t *testing.T) {
	f, _ := newIngestFeature(t, map[string]any{"store_images": true})

	text := f.hist.AddText("写回我")
	missing := f.hist.AddImageFile(filepath.Join(f.ctx.DataDir, "img_missing.png"), 5)

	t.Run("参数错误给出中文错误", func(t *testing.T) {
		cases := []struct {
			name   string
			params map[string]string
			want   string
		}{
			{name: "缺少 id", params: nil, want: "缺少"},
			{name: "非数字 id", params: map[string]string{"id": "x1"}, want: "无效"},
			{name: "不存在的 id", params: map[string]string{"id": "999"}, want: "不存在"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := f.RunAction("write_entry", tc.params)
				if err == nil {
					t.Fatal("期望报错，实际成功")
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("错误信息 %q 未包含 %q", err.Error(), tc.want)
				}
			})
		}
	})

	t.Run("图片条目分派到缓存读取分支", func(t *testing.T) {
		// 缓存文件不存在：所有平台上都会在读取缓存这一步失败——
		// 该确定性错误证明 ID 查找成功且分派进了图片分支。
		err := f.RunAction("write_entry", map[string]string{"id": strconv.Itoa(missing.ID)})
		if err == nil {
			t.Fatal("缓存文件缺失时写回应失败")
		}
		if !strings.Contains(err.Error(), "读取图片缓存失败") {
			t.Errorf("错误信息应来自图片分支的缓存读取：%q", err.Error())
		}
	})

	t.Run("文本条目到达写回层", func(t *testing.T) {
		err := f.RunAction("write_entry", map[string]string{"id": strconv.Itoa(text.ID)})
		if err == nil {
			// 真实剪贴板可用（有显示环境）：写回成功必须标记回声，
			// 否则 watcher 会把这次写回当新内容重新入库。
			if !f.isEchoText("写回我") {
				t.Error("写回成功后未标记回声，历史将出现重复条目")
			}
			return
		}
		// 无显示环境（headless CI）：写回本身失败是环境约束。
		// 关键断言：错误不是查找/参数错误，证明分派已到达剪贴板写回层。
		msg := err.Error()
		for _, marker := range []string{"缺少", "无效", "不存在"} {
			if strings.Contains(msg, marker) {
				t.Fatalf("有效 ID 不应报查找错误：%q", msg)
			}
		}
		t.Logf("无剪贴板环境，写回层按预期报错: %v", err)
	})

	t.Run("文件条目在非 Windows 报中文平台错误", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("仅验证非 Windows 的降级错误")
		}
		file := f.hist.AddFileEntry(filepath.Join("C:\\Users", "a.txt"), "a.txt")
		err := f.RunAction("write_entry", map[string]string{"id": strconv.Itoa(file.ID)})
		if err == nil {
			t.Fatal("非 Windows 下文件条目写回应失败")
		}
		// winui 的 errUnsupported 本身就是中文并注明仅 Windows 支持，
		// 动作层包上条目上下文后一起返回。
		if !strings.Contains(err.Error(), "写回") || !strings.Contains(err.Error(), "仅 Windows") {
			t.Errorf("错误应可读并注明平台限制：%q", err.Error())
		}
	})
}

// TestActionsDeclareHistoryEntries：新动作必须进 Actions() 声明（Group 历史，
// 带 id 参数），面板与 Wails 才能按契约渲染与调用。
func TestActionsDeclareHistoryEntries(t *testing.T) {
	f := NewFeature().(*Feature)
	declared := map[string]core.Action{}
	for _, a := range f.Actions() {
		declared[a.ID] = a
	}

	del, ok := declared["delete_entry"]
	if !ok {
		t.Fatal("Actions() 缺少 delete_entry")
	}
	if del.Group != "历史" {
		t.Errorf("delete_entry.Group = %q，期望 历史", del.Group)
	}
	if !del.Confirm || del.Kind != core.ActionDanger {
		t.Errorf("delete_entry 应为需确认的危险操作：Confirm=%v Kind=%q", del.Confirm, del.Kind)
	}
	if !hasParam(del, "id") {
		t.Error("delete_entry 缺少 id 参数声明")
	}

	wr, ok := declared["write_entry"]
	if !ok {
		t.Fatal("Actions() 缺少 write_entry")
	}
	if wr.Group != "历史" {
		t.Errorf("write_entry.Group = %q，期望 历史", wr.Group)
	}
	if wr.Kind == core.ActionDanger || wr.Confirm {
		t.Error("write_entry 不应标记为危险操作")
	}
	if !hasParam(wr, "id") {
		t.Error("write_entry 缺少 id 参数声明")
	}
}

func hasParam(a core.Action, key string) bool {
	for _, p := range a.Params {
		if p.Key == key {
			return true
		}
	}
	return false
}
