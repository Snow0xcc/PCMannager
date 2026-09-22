//go:build windows

package selfcontext

import (
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// showContext opens a window listing the recent active-window history and a
// button to copy a compact summary for use with an LLM (MineContext style).
func showContext(f *Feature) {
	var mw *walk.MainWindow
	var edit *walk.TextEdit

	refresh := func() {
		edit.SetText(f.Summary())
	}

	MainWindow{
		AssignTo: &mw,
		Title:    "PCMannager - 上下文记录 (MineContext)",
		MinSize:  Size{Width: 520, Height: 420},
		Layout:   VBox{},
		Children: []Widget{
			Label{Text: "自动记录的活动窗口（可复制到 LLM 作为上下文）："},
			TextEdit{
				AssignTo: &edit,
				ReadOnly: true,
				MinSize:  Size{Height: 280},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					PushButton{Text: "刷新", OnClicked: refresh},
					PushButton{
						Text: "复制摘要",
						OnClicked: func() {
							walk.Clipboard().SetText(f.Summary())
						},
					},
				},
			},
		},
	}.Run()
	_ = mw
}
