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
// panel still exposes the saved files.
func openEditor(ctx *core.Context, img *image.RGBA, bounds image.Rectangle, onSave, onCopy func(image.Image)) {
	if ctx == nil {
		return
	}
	ctx.Logger.Warn("当前平台不支持截图编辑窗口", "module", moduleID)
	ctx.Bus.Notice(moduleID, "当前平台不支持截图编辑窗口")
}
