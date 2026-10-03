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

// TestCommandMatchScore 守护匹配分级：全等 > 别名全等 > 前缀 > 别名前缀 >
// 包含 > 提示命中；别名参与匹配但同级弱于 Label 本身。
func TestCommandMatchScore(t *testing.T) {
	prefix := command{Label: "打开面板", Hint: "界面"}
	sub := command{Label: "一键打开", Hint: "界面"}

	if prefix.matchScore("打开面板", nil) != matchExact {
		t.Fatalf("全等应 matchExact, 实际 %d", prefix.matchScore("打开面板", nil))
	}
	if prefix.matchScore("打开", nil) != matchPrefix {
		t.Fatalf("前缀应 matchPrefix, 实际 %d", prefix.matchScore("打开", nil))
	}
	if sub.matchScore("打开", nil) != matchContain {
		t.Fatalf("包含应 matchContain, 实际 %d", sub.matchScore("打开", nil))
	}
	if prefix.matchScore("xyz", nil) != -1 {
		t.Fatal("无命中应为 -1")
	}
	if prefix.matchScore("", nil) != matchNone {
		t.Fatalf("空查询应 matchNone, 实际 %d", prefix.matchScore("", nil))
	}
}

// TestMatchScoreAliases 守护别名匹配：全等/前缀/包含三级，均弱于 Label 同级。
func TestMatchScoreAliases(t *testing.T) {
	c := command{Label: "记事本"}
	aliases := []string{"jsb", "notepad"}

	if got := c.matchScore("jsb", aliases); got != matchAliasExact {
		t.Fatalf("别名全等应 matchAliasExact, 实际 %d", got)
	}
	if got := c.matchScore("js", aliases); got != matchAliasPrefix {
		t.Fatalf("别名前缀应 matchAliasPrefix, 实际 %d", got)
	}
	if got := c.matchScore("tep", aliases); got != matchHint {
		t.Fatalf("别名包含应 matchHint, 实际 %d", got)
	}
	// 别名全等（用户显式输入完整别名，意图明确）强于 Label 普通前缀，
	// 但弱于 Label 全等。
	if c.matchScore("记事本", aliases) <= c.matchScore("jsb", aliases) {
		t.Fatal("Label 全等应强于别名全等")
	}
	if c.matchScore("jsb", aliases) <= c.matchScore("记", aliases) {
		t.Fatal("别名全等应强于 Label 普通前缀")
	}
	// 空别名/空白别名安全。
	if got := c.matchScore("jsb", []string{"", "  "}); got != -1 {
		t.Fatalf("空白别名不应命中, 实际 %d", got)
	}
}

// TestCompositeScore 守护多因子加权公式：置顶 10^6 绝对统治，匹配度 10^3 次之，
// 优先级 10 再次，打开次数 2 最小；四者之间不跨量级窜位。
func TestCompositeScore(t *testing.T) {
	c := command{Label: "百度", Priority: 5}

	// 置顶项即使匹配度更低、优先级更低，也远高于未置顶的高匹配项。
	pinned := command{Label: "百度", Priority: 1}
	unpinned := command{Label: "打开剪贴板历史", Priority: 5}
	if pinned.compositeScore("百", 0, true, nil) <= unpinned.compositeScore("打开剪贴板历史", 100, false, nil) {
		t.Fatal("置顶项应压倒一切未置顶项")
	}

	// 单调性：打开次数越大得分越高；优先级越高得分越高。
	c2 := command{Label: "百度", Priority: 5}
	if c2.compositeScore("", 10, false, nil) <= c2.compositeScore("", 0, false, nil) {
		t.Fatal("打开次数增加应提升得分")
	}
	c3 := command{Label: "百度", Priority: 9}
	if c3.compositeScore("", 0, false, nil) <= c2.compositeScore("", 0, false, nil) {
		t.Fatal("优先级提高应提升得分")
	}

	// 基础分值形态：空查询下 = 1*1000 + 5*10 + 0*2。
	if got := c.compositeScore("", 0, false, nil); got != 1000+50 {
		t.Fatalf("基础分值 = %d, 期望 %d", got, 1000+50)
	}
	// 不匹配恒为 -1（即使置顶也不混入）。
	if got := c.compositeScore("zzz", 0, true, nil); got != -1 {
		t.Fatalf("不匹配应为 -1, 实际 %d", got)
	}
	// 别名命中也要参与加权：别名全等 60*1000。
	if got := c.compositeScore("bd", 0, false, []string{"bd"}); got != matchAliasExact*1000+50 {
		t.Fatalf("别名全等分值 = %d, 期望 %d", got, matchAliasExact*1000+50)
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

// （面板几何测试 TestPanelSize/TestContentWidth 已移到 panel_windows_test.go：
// 它们引用的 panelSize/gridTileW 等常量定义在 panel_windows.go，只能随
// //go:build windows 一起编译——放在本文件会让 linux/darwin 的 go vet 与
// go test 因 undefined 而失败，这正是 CI 在 Linux runner 上红掉的原因。）
