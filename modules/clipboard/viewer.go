//go:build windows

package clipboard

import (
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// showViewer opens a Ditto-style clipboard history window.
func showViewer(hist *History, app *core.App, writeBack func(string)) {
	entries := hist.All()

	previews := make([]string, len(entries))
	for i, e := range entries {
		p := e.Text
		if len(p) > 60 {
			p = p[:60] + "…"
		}
		previews[i] = e.Timestamp.Format("15:04:05") + "  " + singleLine(p)
	}

	var listBox *walk.ListBox
	var preview *walk.TextEdit
	var mw *walk.MainWindow
	current := -1

	onSelect := func() {
		idx := listBox.CurrentIndex()
		if idx < 0 || idx >= len(entries) {
			current = -1
			return
		}
		current = idx
		preview.SetText(entries[idx].Text)
	}

	copy := func() {
		if current < 0 || current >= len(entries) {
			return
		}
		writeBack(entries[current].Text)
		if mw != nil {
			mw.Close()
		}
	}

	MainWindow{
		AssignTo: &mw,
		Title:    "PCMannager - 剪贴板历史 (Ditto)",
		MinSize:  Size{Width: 560, Height: 420},
		Layout:   VBox{},
		Children: []Widget{
			Label{Text: "最近复制的内容（Enter 或双击复制回剪贴板）:"},
			ListBox{
				AssignTo:              &listBox,
				Model:                 previews,
				OnCurrentIndexChanged: onSelect,
				OnItemActivated:       copy,
			},
			TextEdit{
				AssignTo: &preview,
				ReadOnly: true,
				MinSize:  Size{Height: 120},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					PushButton{Text: "复制回剪贴板 (Enter)", OnClicked: copy},
					PushButton{
						Text: "清空历史",
						OnClicked: func() {
							hist.Clear()
							if mw != nil {
								mw.Close()
							}
						},
					},
				},
			},
		},
	}.Run()
}

func singleLine(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\r' {
			c = ' '
		}
		out = append(out, c)
	}
	return string(out)
}
