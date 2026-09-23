//go:build windows

package repair

import (
	"sync"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// repairPanelTitle is also used to find an already-open panel window.
const repairPanelTitle = "PCMannager - 电脑修复与工具安装"

// panelState tracks the single open repair window so OpenUI can focus it
// instead of spawning duplicates, and Stop can close it. The pointer is read
// from the UI goroutine and written from OpenUI/Stop, hence the mutex.
var panelState struct {
	mu sync.Mutex
	mw *walk.MainWindow
}

// setPanel records the currently open panel window.
func setPanel(mw *walk.MainWindow) {
	panelState.mu.Lock()
	defer panelState.mu.Unlock()
	panelState.mw = mw
}

// currentPanel returns the open panel window, if any.
func currentPanel() *walk.MainWindow {
	panelState.mu.Lock()
	defer panelState.mu.Unlock()
	return panelState.mw
}

// openPanel builds the PC-repair / tool-installation window. Every button is
// rendered from the declarative catalogue (catalog.go), so this function holds
// only the layout — never the tool list. Adding a tool means editing the
// catalogue, not this file.
func openPanel(f *Feature) {
	var mw *walk.MainWindow
	var logEdit *walk.TextEdit

	// group renders a titled GroupBox of buttons for one sub-category.
	group := func(title string, entries []Entry) GroupBox {
		buttons := make([]Widget, 0, len(entries))
		for _, e := range entries {
			e := e
			buttons = append(buttons, PushButton{
				Text:      e.Label,
				OnClicked: func() { runAction(f, logEdit, e) },
			})
		}
		return GroupBox{
			Title:  title,
			Layout: VBox{},
			Children: []Widget{
				Composite{Layout: HBox{}, Children: buttons},
			},
		}
	}

	// page bundles the catalogue groups of one tab into a TabPage.
	page := func(title string) TabPage {
		var children []Widget
		for _, g := range GroupsIn(title) {
			children = append(children, group(g, EntriesIn(title, g)))
		}
		return TabPage{Title: title, Layout: VBox{}, Children: children}
	}

	pages := make([]TabPage, 0, len(Pages))
	for _, p := range Pages {
		pages = append(pages, page(p))
	}

	if err := (MainWindow{
		AssignTo: &mw,
		Title:    repairPanelTitle,
		MinSize:  Size{Width: 720, Height: 600},
		Layout:   VBox{},
		Children: []Widget{
			TabWidget{Pages: pages},
			Label{Text: "运行日志:"},
			TextEdit{
				AssignTo: &logEdit,
				ReadOnly: true,
				MinSize:  Size{Height: 140},
				VScroll:  true,
			},
		},
	}).Create(); err != nil {
		if f.ctx != nil && f.ctx.Logger != nil {
			f.ctx.Logger.Error("创建修复面板失败", "module", moduleID, "err", err)
		}
		return
	}

	setPanel(mw)
	mw.Run()
	setPanel(nil)
}

// focusPanel brings an already-open panel to the front.
func (f *Feature) focusPanel() { focusRepairPanel() }

// closePanel closes the panel when the module stops.
func (f *Feature) closePanel() { closeRepairPanel() }

// focusRepairPanel brings an already-open panel to the front.
func focusRepairPanel() {
	mw := currentPanel()
	if mw == nil {
		return
	}
	_ = mw.BringToTop()
	mw.SetVisible(true)
}

// closeRepairPanel asks the panel to close and releases the handle.
//
// The request is posted asynchronously: Stop() runs while holding the feature
// mutex, and a synchronous SendMessage to a message loop that is shutting down
// could deadlock the whole application.
func closeRepairPanel() {
	mw := currentPanel()
	if mw == nil {
		return
	}
	go func() { _ = mw.Close() }()
}

// runAction executes a catalogue entry, confirming dangerous ones first, and
// appends the captured output to the panel log. The command line is resolved
// from the stored package-manager preference so winget/choco stay switchable.
func runAction(f *Feature, logEdit *walk.TextEdit, e Entry) {
	source := ""
	if f != nil {
		source = f.preferSource()
	}
	cmdline, ok := e.ResolveCommand(source)
	if !ok {
		cmdline = e.Command
	}

	if e.Danger && f != nil && f.confirmDanger() && !confirm(f, e.Label) {
		return
	}

	out, err := runCommand(cmdline)
	if f != nil && f.ctx != nil {
		if f.ctx.Logger != nil {
			f.ctx.Logger.Info("repair 执行动作", "module", moduleID, "action", e.ID, "cmd", cmdline)
		}
		if f.ctx.Bus != nil {
			if err != nil {
				f.ctx.Bus.Log(moduleID, "error", e.Label+": "+err.Error())
			} else {
				f.ctx.Bus.Progress(moduleID, e.ID, 100, out)
			}
		}
	}
	if logEdit != nil {
		line := appendLogf(e.Label, out, err)
		if cur := logEdit.Text(); cur == "" {
			_ = logEdit.SetText(line)
		} else {
			_ = logEdit.SetText(cur + "\n" + line)
		}
	}
}

// confirm asks the user before running a dangerous command.
func confirm(f *Feature, name string) bool {
	mw := currentPanel()
	if mw == nil {
		return true
	}
	rc := walk.MsgBox(mw, "确认操作", "确定要执行「"+name+"」吗？", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion)
	if rc == walk.DlgCmdYes && f != nil && f.ctx != nil && f.ctx.Logger != nil {
		f.ctx.Logger.Info("repair 已确认危险操作", "module", moduleID, "action", name)
	}
	return rc == walk.DlgCmdYes
}
