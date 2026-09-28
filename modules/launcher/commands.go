package launcher

import (
	"fmt"
	"sort"
	"strings"

	"github.com/snow0xcc/pcmannager/internal/core"
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
	// opens a module's UI, "url" opens a web address.
	Kind string
	// ModuleID/ActionID address a module action (Kind = action/open).
	ModuleID, ActionID string
	// URL is the address for Kind = url.
	URL string
}

// score rates how well a command matches the query: prefix beats substring
// beats no match (-1). The ranking is deliberately simple — the table is
// a few dozen entries, so a fuzzy index would be architecture theatre.
func (c command) score(q string) int {
	label := strings.ToLower(c.Label)
	switch {
	case strings.HasPrefix(label, q):
		return 0
	case strings.Contains(label, q):
		return 1
	case strings.Contains(strings.ToLower(c.Hint), q):
		return 2
	}
	return -1
}

// key 是命令在排行榜中的稳定标识：模块动作用 <module>/<action>，网页捷径用
// url:<地址>，模块入口用 <module>。跨进程重启后依然一致，使用量/置顶才能持久化。
func (c command) key() string {
	switch c.Kind {
	case "action":
		return c.ModuleID + "/" + c.ActionID
	case "open":
		return c.ModuleID
	case "url":
		return "url:" + c.URL
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
				})
			}
		}
	}

	// 内置网页捷径（uTools 式快捷入口）。
	for _, w := range webShortcuts {
		out = append(out, command{
			Label: w.name, Hint: "网页 · " + w.url,
			Icon: "web", Kind: "url", URL: w.url,
		})
	}

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
// 排序优先级：置顶（固定到前方）永远最前，其次打开次数降序，最后才是文本
// 匹配度（前缀 > 包含 > 提示命中）。空查询同样按此排序，只是不做匹配过滤。
func search(cmds []command, q string, max int, ranks *rankStore) []command {
	q = strings.ToLower(strings.TrimSpace(q))

	type scored struct {
		c    command
		s    int
		pin  bool
		uses int
	}
	var hits []scored
	for _, c := range cmds {
		if q != "" && c.score(q) < 0 {
			continue
		}
		uses, pin := 0, false
		if ranks != nil {
			uses, pin = ranks.get(c.key())
		}
		hits = append(hits, scored{c: c, s: c.score(q), pin: pin, uses: uses})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].pin != hits[j].pin {
			return hits[i].pin
		}
		if hits[i].uses != hits[j].uses {
			return hits[i].uses > hits[j].uses
		}
		return hits[i].s < hits[j].s
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
