//go:build !windows

package screenshot

import (
	"image"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// openEditor is a no-op outside Windows.
//
// A portable editor would need a cgo toolkit, which the release build forbids,
// so the module reports the situation through the event bus instead and the
// panel still exposes the saved files. It takes the same mode and hooks as the
// Windows implementation so the caller needs no build tags of its own.
func openEditor(ctx *core.Context, img *image.RGBA, bounds image.Rectangle, mode edMode, hooks editorHooks) {
	if ctx == nil {
		return
	}
	ctx.Logger.Warn("当前平台不支持截图编辑窗口", "module", moduleID)
	ctx.Bus.Notice(moduleID, "当前平台不支持截图编辑窗口")
}
