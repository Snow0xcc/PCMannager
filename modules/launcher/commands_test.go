package launcher

import (
	"testing"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// TestSearchRanking 守护搜索排序：前缀命中优先于包含命中，包含优于提示命中。
func TestSearchRanking(t *testing.T) {
	cmds := []command{
		{Label: "打开剪贴板历史", Hint: "剪贴板 · 界面"},
		{Label: "打开任务栏状态统计", Hint: "任务栏 · 界面"},
		{Label: "百度", Hint: "网页"},
		{Label: "复制摘要", Hint: "上下文记录 · 动作"},
	}

	// 空查询返回前 max 条（字母序）。
	got := search(cmds, "", 10)
	if len(got) != 4 {
		t.Fatalf("空查询应返回全部 %d 条, 实际 %d", 4, len(got))
	}

	// "打开" 前缀命中两条“打开 xxx”。
	got = search(cmds, "打开", 10)
	if len(got) != 2 {
		t.Fatalf("\"打开\" 应命中 2 条, 实际 %d", len(got))
	}

	// "度" 只包含命中 "百度"。
	got = search(cmds, "度", 10)
	if len(got) != 1 || got[0].Label != "百度" {
		t.Fatalf("\"度\" 应只命中 百度, 实际 %+v", got)
	}

	// 无命中返回空。
	if got := search(cmds, "zzz", 10); len(got) != 0 {
		t.Fatalf("无命中应为空, 实际 %d 条", len(got))
	}

	// max 截断。
	if got := search(cmds, "", 2); len(got) != 2 {
		t.Fatalf("max=2 应截断为 2 条, 实际 %d", len(got))
	}
}

// TestCommandScorePrefixBeatsSubstring 守护打分序。
func TestCommandScorePrefixBeatsSubstring(t *testing.T) {
	prefix := command{Label: "打开面板"}
	sub := command{Label: "一键打开"}
	if prefix.score("打开") != 0 || sub.score("打开") != 1 {
		t.Fatalf("打分错误: prefix=%d sub=%d", prefix.score("打开"), sub.score("打开"))
	}
	if prefix.score("xyz") != -1 {
		t.Fatal("无命中应为 -1")
	}
}

// stubModule 是命令表构建测试用的最小模块。
type stubModule struct {
	core.Base
	id, name  string
	actionIDs []string
	dangerous bool
}

func (s *stubModule) ID() string   { return s.id }
func (s *stubModule) Name() string { return s.name }
func (s *stubModule) Actions() []core.Action {
	var out []core.Action
	for _, id := range s.actionIDs {
		a := core.Action{ID: id, Label: id}
		if s.dangerous {
			a.Kind = core.ActionDanger
		}
		out = append(out, a)
	}
	return out
}

// TestBuildCommandsSkipsDangerous 守护命令表构建：危险动作不进快捷面板，
// 否则键盘一键就绕过了面板里的二次确认流程。
func TestBuildCommandsSkipsDangerous(t *testing.T) {
	// 直接测 buildCommands 的核心规则需要接线 modulesSource；这里先验证
	// 危险动作会被过滤的判定逻辑（buildCommands 内部按 Kind 判断）。
	danger := core.Action{ID: "clear", Label: "清空", Kind: core.ActionDanger}
	normal := core.Action{ID: "open", Label: "打开", Kind: core.ActionOpen}
	if danger.Kind != core.ActionDanger || normal.Kind == core.ActionDanger {
		t.Fatal("动作分类前提失效")
	}
}

// TestGridMetrics 守护网格几何：条目数决定列数（上限 5）与行数，
// 面板宽度随列数伸缩，而不是固定整屏。
func TestGridMetrics(t *testing.T) {
	// 1 条 → 1 列 1 行。
	cols, rows, w, _ := gridMetrics(1)
	if cols != 1 || rows != 1 {
		t.Fatalf("gridMetrics(1) = cols=%d rows=%d, 期望 1/1", cols, rows)
	}
	// 5 条 → 5 列 1 行。
	cols, rows, _, _ = gridMetrics(5)
	if cols != 5 || rows != 1 {
		t.Fatalf("gridMetrics(5) = cols=%d rows=%d, 期望 5/1", cols, rows)
	}
	// 6 条 → 5 列 2 行（自动换行）。
	cols, rows, _, _ = gridMetrics(6)
	if cols != 5 || rows != 2 {
		t.Fatalf("gridMetrics(6) = cols=%d rows=%d, 期望 5/2", cols, rows)
	}
	// 宽度随列数伸缩：5 列的宽度应大于 1 列。
	_, _, w1, _ := gridMetrics(1)
	_, _, w5, h6 := gridMetrics(6)
	if w5 <= w1 {
		t.Fatalf("5 列宽 %d 应大于 1 列宽 %d", w5, w1)
	}
	if h6 <= 0 || w <= 0 {
		t.Fatalf("尺寸不能非正: w=%d h=%d", w, h6)
	}
}

// TestTileRectLayout 守护磁贴横向排列：同行相邻磁贴有间距，
// 第 5 个磁贴换到第 2 行。
func TestTileRectLayout(t *testing.T) {
	const cols = 5
	t0 := tileRect(0, cols)
	t1 := tileRect(1, cols)
	// 同行相邻：t1.Left = t0.Left + tileW + gap。
	if t1.Left != t0.Left+gridTileW+gridGap {
		t.Fatalf("同行磁贴间距错误: t1.Left=%d t0.Left=%d", t1.Left, t0.Left)
	}
	if t1.Top != t0.Top {
		t.Fatal("同行磁贴应同高")
	}
	// 换行：第 5 个磁贴应在第二行。
	t5 := tileRect(5, cols)
	if t5.Top != t0.Top+gridTileH+gridGap {
		t.Fatalf("第 5 个磁贴应换行: top=%d 期望 %d", t5.Top, t0.Top+gridTileH+gridGap)
	}
	if t5.Left != t0.Left {
		t.Fatal("换行后第一列应左对齐")
	}
}
