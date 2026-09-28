package launcher

import (
	"fmt"
	"sort"
	"strings"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/sysutil"
)

// command is one searchable entry of the quick panel: a label (what the user
// types against), a subtitle (where it comes from) and the thing it does.
//
// The table is intentionally flat and rebuilt on module Start: modules come and
// go with their enable/disable cycle, so a stale cached action would either
// fail or (worse) toggle a module the user disabled seconds ago.
type command struct {
	// Label is the primary text, e.g. "打开剪贴板历史".
	Label string
	// Hint is the secondary text, e.g. "剪贴板历史 · 动作".
	Hint string
	// Icon is a glyph key for the tile (see icons_windows.go); empty means the
	// generic module glyph.
	Icon string
	// Kind discriminates the payload: "action" runs a module action, "open"
	// opens a module's UI, "url" opens a web address, "app" launches a local
	// application, "file" opens a document, "folder" opens a directory.
	Kind string
	// ModuleID/ActionID address a module action (Kind = action/open).
	ModuleID, ActionID string
	// URL is the address for Kind = url.
	URL string
	// Path is the filesystem target for Kind = app/file/folder.
	Path string
	// Priority is the developer-preset importance (buildCommands assigns it):
	// it breaks ties within the same match level in the weighted ranking.
	Priority int
}

// 评分权重（uTools 式多因子加权，量级隔离保证低维因子永远翻不了高维因子）：
//
//	Score = isPinned*10^6 + matchScore*10^3 + priority*10 + openCount*2
//
// 置顶具有绝对统治力；文本匹配度次之；开发者预设优先级与打开频次只在同
// 匹配度内部起作用。全整数运算，int 为 64 位，无溢出风险。
const (
	rankPinWeight   = 1_000_000
	rankMatchWeight = 1_000
	rankPrioWeight  = 10
	rankUseWeight   = 2

	matchExact   = 100 // 标题全等
	matchPrefix  = 50  // 标题前缀
	matchContain = 20  // 标题包含
	matchHint    = 10  // 副标题命中
	matchNone    = 1   // 空查询：全员同分，交给 priority/openCount 排序

	matchAliasExact  = 60 // 别名全等（用户显式起的名字）
	matchAliasPrefix = 40 // 别名前缀（拼音缩写的主要用法）
)

// matchScore rates how well a command matches the query: exact beats prefix
// beats substring beats hint hit; no match is -1. An empty query gives every
// command the same base score so the default list is ordered by priority and
// usage alone.
//
// aliases 是用户手动添加的别名/拼音首字母缩写（如给"记事本"加 "jsb"）：
// 与 Label 同等参与匹配，但同级命中弱于 Label 本身（别名包含只给 matchHint，
// 避免别名喧宾夺主）。
func (c command) matchScore(q string, aliases []string) int {
	if q == "" {
		return matchNone
	}
	label := strings.ToLower(c.Label)
	switch {
	case label == q:
		return matchExact
	case strings.HasPrefix(label, q):
		return matchPrefix
	case strings.Contains(label, q):
		return matchContain
	case strings.Contains(strings.ToLower(c.Hint), q):
		return matchHint
	}
	// 别名匹配：全等/前缀是独立强度（用户显式起的名字），包含则弱于提示。
	for _, a := range aliases {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		switch {
		case a == q:
			return matchAliasExact
		case strings.HasPrefix(a, q):
			return matchAliasPrefix
		case strings.Contains(a, q):
			return matchHint
		}
	}
	return -1
}

// compositeScore folds the four ranking factors into one comparable score.
// Non-matching commands return -1 even when pinned (置顶只在“匹配”的前提下
// 统治排序，不会让无关项混进搜索结果).
func (c command) compositeScore(q string, openCount int, pinned bool, aliases []string) int {
	m := c.matchScore(q, aliases)
	if m < 0 {
		return -1
	}
	s := m * rankMatchWeight
	if pinned {
		s += rankPinWeight
	}
	s += c.Priority * rankPrioWeight
	s += openCount * rankUseWeight
	return s
}

// key 是命令在排行榜中的稳定标识：模块动作用 <module>/<action>，网页捷径用
// url:<地址>，模块入口用 <module>，本地应用/文件/文件夹用 path:<路径>。跨进程
// 重启后依然一致，使用量/置顶才能持久化。
func (c command) key() string {
	switch c.Kind {
	case "action":
		return c.ModuleID + "/" + c.ActionID
	case "open":
		return c.ModuleID
	case "url":
		return "url:" + c.URL
	case "app", "file", "folder":
		return "path:" + c.Path
	}
	return c.Label
}

// buildCommands assembles the searchable table from live modules plus a set of
// built-in web shortcuts.
//
// The module-derived entries reuse the module's own Actions()/OpenUI paths, so
// the panel never gains a capability the module does not have — the launcher is
// a keyboard-shaped window over the same registry the panel drives.
func buildCommands(ctx *core.Context) []command {
	var out []command

	if ctx != nil {
		for _, m := range registryModules(ctx) {
			name := m.Name()
			if name == "" {
				name = m.ID()
			}
			out = append(out, command{
				Label: "打开 " + name, Hint: name + " · 界面",
				Icon: iconForModule(m.ID()), Kind: "open", ModuleID: m.ID(),
				Priority: 5, // 界面入口是首屏最该一眼认出的项
			})
			for _, a := range m.Actions() {
				if a.Kind == core.ActionDanger {
					// 危险动作（清空历史等）不适合键盘一键触发：
					// 面板里的二次确认流程在这里不存在，宁缺勿滥。
					continue
				}
				label := a.Label
				if label == "" {
					label = a.ID
				}
				out = append(out, command{
					Label: label, Hint: name + " · 动作",
					Icon: "bolt", Kind: "action", ModuleID: m.ID(), ActionID: a.ID,
					Priority: 3,
				})
			}
		}
	}

	// 内置网页捷径（uTools 式快捷入口）。
	for _, w := range webShortcuts {
		out = append(out, command{
			Label: w.name, Hint: "网页 · " + w.url,
			Icon: "web", Kind: "url", URL: w.url,
			Priority: 4,
		})
	}

	// 本地应用（开始菜单 .lnk）。扫描失败/为空不致命——面板仍靠模块动作与
	// 网页捷径工作，这里只是把启动器做成真正的"应用启动器"。
	out = append(out, appEntries()...)

	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	// 空查询的默认列表把「打开 Xxx」模块入口排在动作前面：磁贴一行只有
	// 5 个，首屏应该是一眼能认出的功能入口，而不是几十个安装动作。
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := out[i].Kind == "open", out[j].Kind == "open"
		if pi != pj {
			return pi
		}
		return false
	})
	return out
}

// iconForModule maps a module id to a glyph key so each module's entry gets a
// recognisable tile instead of a wall of identical boxes.
func iconForModule(id string) string {
	switch id {
	case "screenshot":
		return "camera"
	case "clipboard":
		return "clipboard"
	case "taskbar":
		return "monitor"
	case "repair":
		return "wrench"
	case "updater":
		return "download"
	case "launcher":
		return "search"
	case "selfcontext":
		return "history"
	default:
		return "module"
	}
}

type webShortcut struct{ name, url string }

// webShortcuts are the built-ins; keeping the list tiny and obvious.
var webShortcuts = []webShortcut{
	{"百度", "https://www.baidu.com"},
	{"必应搜索", "https://www.bing.com"},
	{"GitHub", "https://github.com"},
}

// registryModules pulls the live module list through a wiring-time bridge
// (setModulesSource in main.go), because core.Context deliberately does not
// expose the registry; nil is handled gracefully so tests can run unwired.
func registryModules(*core.Context) []core.Module {
	if modulesSource == nil {
		return nil
	}
	return modulesSource()
}

// modulesSource is the wiring seam for the live module list.
var modulesSource func() []core.Module

// SetModulesSource lets the application provide the live module list for the
// command table. Passing nil restores the unwired (test) behaviour.
func SetModulesSource(src func() []core.Module) { modulesSource = src }

// run executes a picked command.
func (f *Feature) run(c command) error {
	switch c.Kind {
	case "url":
		return openURL(c.URL)
	case "open":
		if m := f.lookupModule(c.ModuleID); m != nil {
			return f.guarded(c.ModuleID, m.OpenUI)
		}
	case "action":
		if m := f.lookupModule(c.ModuleID); m != nil {
			r, ok := m.(interface {
				RunAction(string, map[string]string) error
			})
			if !ok {
				return fmt.Errorf("launcher: 模块 %s 不支持动作", c.ModuleID)
			}
			return f.guarded(c.ModuleID, func() error { return r.RunAction(c.ActionID, nil) })
		}
	case "app", "file", "folder":
		// 本地应用/文件/文件夹统一经 sysutil.OpenURL（ShellExecute/xdg-open）：
		// .lnk 快捷方式同样能直接启动，无需先解析其目标。
		return sysutil.OpenURL(c.Path)
	default:
		return fmt.Errorf("launcher: 未知命令类型 %s", c.Kind)
	}
	return fmt.Errorf("launcher: 模块 %s 不可用", c.ModuleID)
}

// lookupModule finds a live module from the wired source.
func (f *Feature) lookupModule(id string) core.Module {
	for _, m := range registryModules(f.ctx) {
		if m.ID() == id {
			return m
		}
	}
	return nil
}

// guarded wraps a module call with the app-level recover semantics the rest of
// the codebase relies on: a panicking module must not take the panel down.
func (f *Feature) guarded(id string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("launcher: 模块 %s 执行异常: %v", id, r)
		}
	}()
	return fn()
}

// search returns the commands matching q, best first, capped at max.
//
// 排序用多因子加权公式（见 compositeScore）：置顶 10^6 绝对统治，其次文本
// 匹配度 10^3，再次开发者预设优先级 10，最后打开次数 2。空查询同样按此
// 排序，只是不做匹配过滤。
func search(cmds []command, q string, max int, ranks *rankStore) []command {
	q = strings.ToLower(strings.TrimSpace(q))

	type scored struct {
		c command
		s int
	}
	var hits []scored
	for _, c := range cmds {
		uses, pin, aliases := 0, false, []string(nil)
		if ranks != nil {
			uses, pin, aliases = ranks.get(c.key())
		}
		if s := c.compositeScore(q, uses, pin, aliases); s >= 0 {
			hits = append(hits, scored{c: c, s: s})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].s != hits[j].s {
			return hits[i].s > hits[j].s
		}
		// 同分稳定：保持 buildCommands 的稳定预排序（模块入口在前、字母序）。
		return false
	})
	if len(hits) > max {
		hits = hits[:max]
	}
	out := make([]command, len(hits))
	for i, h := range hits {
		out[i] = h.c
	}
	return out
}
