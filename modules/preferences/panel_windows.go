//go:build windows

package preferences

import (
	"fmt"
	"strconv"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// Show opens the unified preferences panel for PCMannager. Every module can be
// toggled independently, assigned its own global hotkey and tuned through the
// options each module declares -- no per-module hardcoded fields.
func Show(mgr *Manager) {
	a := mgr.App()
	if a == nil || a.Config() == nil {
		return
	}

	var mw *walk.MainWindow

	pages := make([]TabPage, 0, 8)
	for _, mod := range a.Modules() {
		pages = append(pages, modulePage(mgr, mod))
	}
	if len(pages) == 0 {
		pages = append(pages, TabPage{
			Title:    "模块",
			Layout:   VBox{},
			Children: []Widget{Label{Text: "当前没有注册任何模块。"}},
		})
	}

	if err := (MainWindow{
		AssignTo: &mw,
		Title:    "PCMannager - 首选项",
		MinSize:  Size{Width: 480, Height: 420},
		Layout:   VBox{},
		Children: []Widget{
			TabWidget{Pages: pages},
			Composite{Layout: HBox{}, Children: []Widget{
				PushButton{Text: "保存", OnClicked: func() {
					// Hotkeys are rebound so edited hotkeys take effect
					// immediately, without restarting the application.
					a.RebindHotkeys()
					if mw != nil {
						mw.Close()
					}
				}},
				PushButton{Text: "取消", OnClicked: func() {
					if mw != nil {
						mw.Close()
					}
				}},
			}},
		},
	}).Create(); err != nil {
		if log := mgr.Log(); log != nil {
			log.Error("创建首选项窗口失败", "err", err)
		}
		return
	}

	mw.Run()
}

// modulePage builds one tab: enable switch, hotkey entry and every declared
// option rendered as its matching widget.
func modulePage(mgr *Manager, mod core.Module) TabPage {
	a := mgr.App()
	id := mod.ID()
	view := a.Config().Module(id)

	children := []Widget{
		enableToggle(mgr, id, view.Enabled()),
		hotkeyRow(mgr, id, view.Hotkey()),
		Label{Text: "格式示例: ctrl+alt+v / f1 / ctrl+shift+c"},
	}

	for _, opt := range mod.Options() {
		if w, ok := optionWidget(mgr, id, opt); ok {
			children = append(children, w)
		}
	}

	title := mod.Name()
	if title == "" {
		title = id
	}
	return TabPage{Title: title, Layout: VBox{}, Children: children}
}

// enableToggle renders the module on/off switch. Toggling goes through
// App.EnableModule so the module is really started/stopped, not just flagged.
func enableToggle(mgr *Manager, id string, enabled bool) CheckBox {
	var cb *walk.CheckBox
	return CheckBox{
		AssignTo: &cb,
		Text:     "启用该模块",
		Checked:  enabled,
		OnCheckedChanged: func() {
			if cb == nil {
				return
			}
			if err := mgr.App().EnableModule(id, cb.Checked()); err != nil {
				logErr(mgr, "切换模块启用状态失败", id, err)
			}
		},
	}
}

// hotkeyRow renders the hotkey entry plus validation feedback.
func hotkeyRow(mgr *Manager, id, current string) Composite {
	var le *walk.LineEdit
	return Composite{
		Layout: HBox{},
		Children: []Widget{
			Label{Text: "快捷键:"},
			LineEdit{
				AssignTo: &le,
				Text:     current,
				OnEditingFinished: func() {
					if le == nil {
						return
					}
					hk := le.Text()
					if hk != "" && !core.ValidHotkey(hk) {
						logErr(mgr, "热键格式无效", id, fmt.Errorf("无效热键: %s", hk))
						return
					}
					a := mgr.App()
					if err := a.Config().Module(id).SetHotkey(hk); err != nil {
						logErr(mgr, "保存热键失败", id, err)
						return
					}
					// Rebinding picks the new value straight from config.
					a.RebindHotkeys()
				},
			},
		},
	}
}

// optionWidget renders one declarative option as its matching widget, reading
// and writing through config.Manager/ModuleView.
func optionWidget(mgr *Manager, id string, opt core.Option) (Widget, bool) {
	a := mgr.App()
	view := a.Config().Module(id)

	label := opt.Label
	if label == "" {
		label = opt.Key
	}

	switch opt.Kind {
	case core.KindBool:
		var cb *walk.CheckBox
		def, _ := opt.Default.(bool)
		checked, _ := view.Get(opt.Key, def).(bool)
		return CheckBox{
			AssignTo: &cb,
			Text:     label,
			Checked:  checked,
			OnCheckedChanged: func() {
				if cb == nil {
					return
				}
				on := cb.Checked()
				if err := view.Set(opt.Key, on); err != nil {
					logErr(mgr, "保存配置项失败", id, err)
					return
				}
				if err := a.ApplyOption(id, opt.Key, on); err != nil {
					logErr(mgr, "应用配置项失败", id, err)
				}
			},
		}, true

	case core.KindSelect:
		var cb *walk.ComboBox
		labels := make([]string, 0, len(opt.Choices))
		values := make([]string, 0, len(opt.Choices))
		for _, c := range opt.Choices {
			labels = append(labels, c.Label)
			values = append(values, c.Value)
		}
		cur := fmt.Sprint(view.Get(opt.Key, opt.Default))
		current := -1
		for i, v := range values {
			if v == cur {
				current = i
				break
			}
		}
		return Composite{
			Layout: HBox{},
			Children: []Widget{
				Label{Text: label + ":"},
				ComboBox{
					AssignTo:     &cb,
					Model:        labels,
					CurrentIndex: current,
					OnCurrentIndexChanged: func() {
						if cb == nil {
							return
						}
						if i := cb.CurrentIndex(); i >= 0 && i < len(values) {
							if err := view.Set(opt.Key, values[i]); err != nil {
								logErr(mgr, "保存配置项失败", id, err)
								return
							}
							if err := a.ApplyOption(id, opt.Key, values[i]); err != nil {
								logErr(mgr, "应用配置项失败", id, err)
							}
						}
					},
				},
			},
		}, true

	default:
		// String/int/color fall back to a text field; the web panel renders
		// richer widgets from the same descriptors.
		var le *walk.LineEdit
		return Composite{
			Layout: HBox{},
			Children: []Widget{
				Label{Text: label + ":"},
				LineEdit{
					AssignTo: &le,
					Text:     formatValue(view.Get(opt.Key, opt.Default)),
					OnEditingFinished: func() {
						if le == nil {
							return
						}
						raw := le.Text()
						v, err := parseValue(opt, raw)
						if err != nil {
							logErr(mgr, "配置项格式无效", id, err)
							return
						}
						if err := view.Set(opt.Key, v); err != nil {
							logErr(mgr, "保存配置项失败", id, err)
							return
						}
						if err := a.ApplyOption(id, opt.Key, v); err != nil {
							logErr(mgr, "应用配置项失败", id, err)
						}
					},
				},
			},
		}, true
	}
}

// parseValue converts raw text back into the option's declared type.
func parseValue(opt core.Option, raw string) (any, error) {
	switch opt.Kind {
	case core.KindInt:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("%s 需要整数: %w", opt.Key, err)
		}
		if opt.Min != 0 || opt.Max != 0 {
			if n < opt.Min || n > opt.Max {
				return nil, fmt.Errorf("%s 超出范围 [%d, %d]", opt.Key, opt.Min, opt.Max)
			}
		}
		return n, nil
	default:
		return raw, nil
	}
}

// logErr reports a panel failure through the application logger and bus.
func logErr(mgr *Manager, msg, id string, err error) {
	if log := mgr.Log(); log != nil {
		log.Error(msg, "module", id, "err", err)
	}
}
