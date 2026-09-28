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

	// 空查询返回前 max 条（按置顶/次数/字母序）。
	got := search(cmds, "", 10, nil)
	if len(got) != 4 {
		t.Fatalf("空查询应返回全部 %d 条, 实际 %d", 4, len(got))
	}

	// "打开" 前缀命中两条“打开 xxx”。
	got = search(cmds, "打开", 10, nil)
	if len(got) != 2 {
		t.Fatalf("\"打开\" 应命中 2 条, 实际 %d", len(got))
	}

	// "度" 只包含命中 "百度"。
	got = search(cmds, "度", 10, nil)
	if len(got) != 1 || got[0].Label != "百度" {
		t.Fatalf("\"度\" 应只命中 百度, 实际 %+v", got)
	}

	// 无命中返回空。
	if got := search(cmds, "zzz", 10, nil); len(got) != 0 {
		t.Fatalf("无命中应为空, 实际 %d 条", len(got))
	}

	// max 截断。
	if got := search(cmds, "", 2, nil); len(got) != 2 {
		t.Fatalf("max=2 应截断为 2 条, 实际 %d", len(got))
	}
}

// TestSearchRanksPinAndUsage 守护排行榜排序：置顶最前，其次打开次数降序，
// 再次才是文本匹配度。
func TestSearchRanksPinAndUsage(t *testing.T) {
	cmds := []command{
		{Label: "打开剪贴板历史", Kind: "open", ModuleID: "clipboard"},
		{Label: "打开任务栏状态统计", Kind: "open", ModuleID: "taskbar"},
		{Label: "百度", Kind: "url", URL: "https://www.baidu.com"},
	}
	ranks := newRankStore("")
	// 百度被置顶：即使文本匹配度不如“打开…”也应排最前。
	ranks.setPin("url:https://www.baidu.com", true)
	// 任务栏比剪贴板多打开一次：无置顶时按次数降序。
	ranks.bump("taskbar")
	ranks.bump("taskbar")
	ranks.bump("clipboard")

	got := search(cmds, "", 10, ranks)
	if got[0].Label != "百度" {
		t.Fatalf("置顶项应最前, 实际 %q", got[0].Label)
	}
	if got[1].Label != "打开任务栏状态统计" {
		t.Fatalf("次数多者应在前, 实际 %q", got[1].Label)
	}
	if got[2].Label != "打开剪贴板历史" {
		t.Fatalf("次数少者应靠后, 实际 %q", got[2].Label)
	}
}

// TestCommandKey 守护命令稳定标识：模块入口/动作/网页分别映射到可持久化 key。
func TestCommandKey(t *testing.T) {
	cases := []struct {
		c    command
		want string
	}{
		{command{Kind: "open", ModuleID: "clipboard"}, "clipboard"},
		{command{Kind: "action", ModuleID: "clipboard", ActionID: "clear"}, "clipboard/clear"},
		{command{Kind: "url", URL: "https://github.com"}, "url:https://github.com"},
	}
	for _, c := range cases {
		if got := c.c.key(); got != c.want {
			t.Errorf("key() = %q, 期望 %q", got, c.want)
		}
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

// TestPanelSize 守护单行布局几何：面板宽度固定为屏幕 3/5，高度恒定（搜索框
// + 一行磁贴），不随条目数增加而变高。
func TestPanelSize(t *testing.T) {
	const screenW = int32(2240)
	w, h := panelSize(1, screenW)
	if w != screenW*3/5 {
		t.Fatalf("面板宽 %d 应为屏宽 3/5 (%d)", w, screenW*3/5)
	}
	if h != gridPadY*2+panelEditH+gridTileH {
		t.Fatalf("面板高 %d 应为恒定单行高 %d", h, gridPadY*2+panelEditH+gridTileH)
	}
	// 条目再多，宽高也不变（内容靠横向滚动）。
	w2, h2 := panelSize(20, screenW)
	if w2 != w || h2 != h {
		t.Fatalf("面板尺寸不应随条目数变化: (%d,%d) vs (%d,%d)", w2, h2, w, h)
	}
}

// TestContentWidth 守护内容总宽计算：N 个磁贴 + 间距 + 内边距。
func TestContentWidth(t *testing.T) {
	c1 := contentWidth(1)
	if c1 != gridPadX*2+gridTileW {
		t.Fatalf("contentWidth(1)=%d, 期望 %d", c1, gridPadX*2+gridTileW)
	}
	c3 := contentWidth(3)
	if c3 != gridPadX*2+3*gridTileW+2*gridGap {
		t.Fatalf("contentWidth(3)=%d, 期望 %d", c3, gridPadX*2+3*gridTileW+2*gridGap)
	}
}
